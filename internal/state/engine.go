package state

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"sync"
	"time"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// SnapshotSource is the poll target. *herdr.Client satisfies it.
type SnapshotSource interface {
	Snapshot(ctx context.Context) (*herdr.Snapshot, error)
}

// Defaults for cadence and per-client queue bounds.
const (
	DefaultPollHot       = 1500 * time.Millisecond
	DefaultPollCold      = 12 * time.Second
	MinPollHot           = 250 * time.Millisecond
	DefaultMaxQueueItems = 8
	DefaultMaxQueueBytes = 4 << 20 // 4 MiB
)

// Config configures an [Engine].
type Config struct {
	Source        SnapshotSource
	Clock         Clock
	PollHot       time.Duration
	PollCold      time.Duration
	MaxQueueItems int
	MaxQueueBytes int
}

// Engine is the poll-as-truth state engine.
type Engine struct {
	source   SnapshotSource
	clock    Clock
	pollHot  time.Duration
	pollCold time.Duration
	maxItems int
	maxBytes int

	wake chan struct{}

	mu          sync.Mutex
	current     *Snapshot
	generations map[string]uint64
	occupants   map[string]string
	seq         uint64
	subs        map[*Subscription]struct{}
	lastPollErr error
	lastPollAt  time.Time
	polls       uint64 // completed polls, for tests/metrics
}

// New builds an [Engine]. It validates and clamps configuration.
func New(cfg Config) (*Engine, error) {
	if cfg.Source == nil {
		return nil, errors.New("state: Source is required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock
	}
	hot := cfg.PollHot
	if hot <= 0 {
		hot = DefaultPollHot
	}
	if hot < MinPollHot {
		hot = MinPollHot
	}
	cold := cfg.PollCold
	if cold <= 0 {
		cold = DefaultPollCold
	}
	if cold < hot {
		cold = hot
	}
	items := cfg.MaxQueueItems
	if items <= 0 {
		items = DefaultMaxQueueItems
	}
	bytes := cfg.MaxQueueBytes
	if bytes <= 0 {
		bytes = DefaultMaxQueueBytes
	}
	return &Engine{
		source:      cfg.Source,
		clock:       clock,
		pollHot:     hot,
		pollCold:    cold,
		maxItems:    items,
		maxBytes:    bytes,
		wake:        make(chan struct{}, 1),
		generations: map[string]uint64{},
		occupants:   map[string]string{},
		subs:        map[*Subscription]struct{}{},
	}, nil
}

// Wake requests an immediate poll. It never blocks; bursts coalesce.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

type pollOutcome struct {
	snap *herdr.Snapshot
	err  error
}

// Run drives the poll loop until ctx is canceled, returning ctx.Err().
func (e *Engine) Run(ctx context.Context) error {
	ticker := e.clock.NewTicker(e.pollCold)
	defer ticker.Stop()

	done := make(chan pollOutcome, 1)
	inProgress := false
	queued := false

	trigger := func() {
		if inProgress {
			queued = true
			return
		}
		inProgress = true
		go func() {
			snap, err := e.source.Snapshot(ctx)
			done <- pollOutcome{snap: snap, err: err}
		}()
	}

	trigger() // poll once at startup

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C():
			trigger()
		case <-e.wake:
			// Debounced immediate poll: coalesced by the in-progress guard and
			// the single queued follow-up.
			trigger()
		case out := <-done:
			inProgress = false
			e.apply(out)
			ticker.Reset(e.desiredInterval())
			if queued {
				queued = false
				trigger()
			}
		}
	}
}

// desiredInterval returns the hot interval when any agent is active or a client
// is subscribed, otherwise the cold interval.
func (e *Engine) desiredInterval() time.Duration {
	if e.active() {
		return e.pollHot
	}
	return e.pollCold
}

func (e *Engine) active() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.subs) > 0 {
		return true
	}
	if e.current == nil || e.current.Topology == nil {
		return false
	}
	topo := e.current.Topology
	// A working/blocked agent must force the hot cadence even when the workspace
	// rollup lags at idle. The authoritative agent list and per-pane status are
	// checked in addition to the workspace rollups (SPEC §11).
	for _, w := range topo.Workspaces {
		if w.AgentStatus.Active() {
			return true
		}
	}
	for _, a := range topo.Agents {
		if a.AgentStatus.Active() {
			return true
		}
	}
	for _, p := range topo.Panes {
		if p.AgentStatus.Active() {
			return true
		}
	}
	return false
}

// apply integrates a poll outcome: it records errors without discarding the
// last good state, and on a changed topology it bumps generations, produces a
// new snapshot, and broadcasts.
func (e *Engine) apply(out pollOutcome) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.polls++
	e.lastPollAt = e.clock.Now()
	if out.err != nil {
		// Poll-as-truth: a transient error leaves the last good snapshot intact.
		e.lastPollErr = out.err
		return
	}
	e.lastPollErr = nil
	topo := out.snap
	if topo == nil {
		return
	}

	e.updateGenerationsLocked(topo)

	hash := hashTopology(topo)
	if e.current != nil && e.current.Hash == hash {
		return // no meaningful change; do not rebroadcast
	}

	gens := make(map[string]uint64, len(e.generations))
	maps.Copy(gens, e.generations)
	e.seq++
	snap := &Snapshot{
		Seq:         e.seq,
		Hash:        hash,
		Topology:    topo,
		Generations: gens,
	}
	if b, err := json.Marshal(snap); err == nil {
		snap.bytes = len(b)
	} else {
		// Conservative accounting: an unsized snapshot must not be treated as
		// free (which would defeat the byte bound). Charge it the maximum so it
		// trips every reasonable per-client budget rather than slipping through.
		snap.bytes = math.MaxInt
	}
	e.current = snap
	e.broadcastLocked(snap)
}

// updateGenerationsLocked bumps the lifecycle generation of each pane whose
// terminal occupant changed, assigns a fresh generation to new panes, and drops
// panes that are gone (exited, closed, or moved to a new id). A dropped id's
// generation lookup fails, so any mutation still holding it is rejected.
func (e *Engine) updateGenerationsLocked(topo *herdr.Snapshot) {
	present := make(map[string]struct{}, len(topo.Panes))
	for _, p := range topo.Panes {
		present[p.PaneID] = struct{}{}
		fp := p.OccupantFingerprint()
		if _, ok := e.generations[p.PaneID]; !ok {
			e.generations[p.PaneID] = 1
			e.occupants[p.PaneID] = fp
			continue
		}
		if e.occupants[p.PaneID] != fp {
			e.generations[p.PaneID]++
			e.occupants[p.PaneID] = fp
		}
	}
	for id := range e.generations {
		if _, ok := present[id]; !ok {
			delete(e.generations, id)
			delete(e.occupants, id)
		}
	}
}

func (e *Engine) broadcastLocked(snap *Snapshot) {
	for sub := range e.subs {
		if !sub.enqueue(snap) {
			// Overflowed or closed; drop it so it cannot wedge the engine.
			delete(e.subs, sub)
		}
	}
}

// Current returns the latest snapshot, or nil before the first successful poll.
func (e *Engine) Current() *Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.current
}

// Generation returns a live pane's lifecycle generation. ok is false when the
// pane is unknown or gone, which callers must treat as a failed guard.
func (e *Engine) Generation(paneID string) (uint64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.generations[paneID]
	return g, ok
}

// CheckGeneration reports whether expected matches the pane's current
// generation. A gone pane never matches.
func (e *Engine) CheckGeneration(paneID string, expected uint64) bool {
	g, ok := e.Generation(paneID)
	return ok && g == expected
}

// Subscribe registers a client queue, seeded with the current snapshot through
// the normal enqueue path so the seed obeys the same byte/item bounds. A seed
// that already exceeds the client's budget overflows the subscription and it is
// not registered.
func (e *Engine) Subscribe() *Subscription {
	e.mu.Lock()
	defer e.mu.Unlock()
	sub := newSubscription(e.maxItems, e.maxBytes)
	if e.current != nil {
		if !sub.enqueue(e.current) {
			return sub // overflowed at seed; already closed, not registered
		}
	}
	e.subs[sub] = struct{}{}
	return sub
}

// Unsubscribe removes and closes a client queue.
func (e *Engine) Unsubscribe(sub *Subscription) {
	e.mu.Lock()
	delete(e.subs, sub)
	e.mu.Unlock()
	sub.Close()
}

// Stats exposes counters for observability and tests.
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Stats{
		Seq:         e.seq,
		Polls:       e.polls,
		Subscribers: len(e.subs),
		LastPollErr: e.lastPollErr,
		LastPollAt:  e.lastPollAt,
	}
}

// Stats is a point-in-time view of engine counters.
type Stats struct {
	Seq         uint64
	Polls       uint64
	Subscribers int
	LastPollErr error
	LastPollAt  time.Time
}
