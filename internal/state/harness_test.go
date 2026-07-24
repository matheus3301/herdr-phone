package state

import (
	"context"
	"sync"
	"time"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// fakeClock is a deterministic Clock with a single controllable ticker (the
// engine creates exactly one and Reset-s it).
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	ticker *fakeTicker
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(d time.Duration) Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{c: make(chan time.Time, 1), interval: d}
	c.ticker = t
	return t
}

// tick fires the current ticker once.
func (c *fakeClock) tick() {
	c.mu.Lock()
	t := c.ticker
	c.mu.Unlock()
	if t == nil {
		return
	}
	select {
	case t.c <- c.Now():
	default:
	}
}

// interval returns the current ticker's configured interval.
func (c *fakeClock) interval() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ticker == nil {
		return 0
	}
	c.ticker.mu.Lock()
	defer c.ticker.mu.Unlock()
	return c.ticker.interval
}

type fakeTicker struct {
	c        chan time.Time
	mu       sync.Mutex
	interval time.Duration
	stopped  bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.c }

func (t *fakeTicker) Reset(d time.Duration) {
	t.mu.Lock()
	t.interval = d
	t.mu.Unlock()
}

func (t *fakeTicker) Stop() {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
}

// fakeSource is a controllable SnapshotSource. It can optionally gate each call
// so a test can hold a poll "in progress".
type fakeSource struct {
	mu      sync.Mutex
	snap    *herdr.Snapshot
	err     error
	calls   int
	entered chan struct{} // signaled on each call entry (buffered)
	gate    chan struct{} // if non-nil, each call blocks until it receives
}

func newSource(snap *herdr.Snapshot) *fakeSource {
	return &fakeSource{snap: snap, entered: make(chan struct{}, 64)}
}

func (s *fakeSource) Snapshot(ctx context.Context) (*herdr.Snapshot, error) {
	s.mu.Lock()
	s.calls++
	gate := s.gate
	snap := s.snap
	err := s.err
	s.mu.Unlock()

	select {
	case s.entered <- struct{}{}:
	default:
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return snap, err
}

func (s *fakeSource) set(snap *herdr.Snapshot, err error) {
	s.mu.Lock()
	s.snap = snap
	s.err = err
	s.mu.Unlock()
}

func (s *fakeSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *fakeSource) enableGate() chan struct{} {
	g := make(chan struct{})
	s.mu.Lock()
	s.gate = g
	s.mu.Unlock()
	return g
}

func (s *fakeSource) disableGate() {
	s.mu.Lock()
	s.gate = nil
	s.mu.Unlock()
}

// --- snapshot builders --------------------------------------------------------

func pane(id, agent, session string, status herdr.AgentStatus) herdr.Pane {
	p := herdr.Pane{PaneID: id, TerminalID: "term_" + id, WorkspaceID: "w1", TabID: "w1:t1", Agent: agent, AgentStatus: status}
	if session != "" {
		p.AgentSession = &herdr.AgentSession{Source: "herdr:" + agent, Agent: agent, Kind: "id", Value: session}
	}
	return p
}

func snapshotWith(wsStatus herdr.AgentStatus, panes ...herdr.Pane) *herdr.Snapshot {
	return &herdr.Snapshot{
		Version:  "0.7.5",
		Protocol: 17,
		Workspaces: []herdr.Workspace{
			{WorkspaceID: "w1", Number: 1, Label: "w1", AgentStatus: wsStatus, ActiveTabID: "w1:t1"},
		},
		Tabs:  []herdr.Tab{{TabID: "w1:t1", WorkspaceID: "w1", Number: 1, Label: "1", AgentStatus: wsStatus}},
		Panes: panes,
	}
}
