package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"time"
)

// Subscription is one events.subscribe entry. Only the fields relevant to
// waking the state engine are modeled; Type is mandatory.
type Subscription struct {
	Type        string `json:"type"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`
	PaneID      string `json:"pane_id,omitempty"`
}

// LifecycleSubscriptions returns the session-wide structural subscriptions the
// state engine uses as poll wakeups. Every entry is accepted by Herdr without a
// scope id. Agent status is intentionally left to the polling cadence: a missed
// status event costs at most one hot interval, and snapshots stay authoritative.
func LifecycleSubscriptions() []Subscription {
	types := []string{
		"workspace.created", "workspace.closed", "workspace.renamed",
		"workspace.moved", "workspace.focused",
		"worktree.created", "worktree.opened", "worktree.removed",
		"tab.created", "tab.closed", "tab.renamed", "tab.moved", "tab.focused",
		"pane.created", "pane.closed", "pane.updated", "pane.focused",
		"pane.moved", "pane.exited", "pane.agent_detected",
		"layout.updated",
	}
	subs := make([]Subscription, len(types))
	for i, t := range types {
		subs[i] = Subscription{Type: t}
	}
	return subs
}

type eventsSubscribeParams struct {
	Subscriptions []Subscription `json:"subscriptions"`
}

// Backoff bounds reconnect delay for the subscriber.
type Backoff struct {
	Initial time.Duration
	Max     time.Duration
	Factor  float64
}

// DefaultBackoff is the reconnect policy used when none is supplied.
var DefaultBackoff = Backoff{Initial: 500 * time.Millisecond, Max: 15 * time.Second, Factor: 2.0}

// DefaultStableSubscription is how long a subscription must stay up (once
// started) before it counts as stable enough to reset the reconnect backoff. A
// subscription that delivers a real event resets immediately regardless.
const DefaultStableSubscription = 5 * time.Second

func (b Backoff) normalized() Backoff {
	if b.Initial <= 0 {
		b.Initial = DefaultBackoff.Initial
	}
	if b.Max < b.Initial {
		b.Max = b.Initial
	}
	if b.Factor < 1 {
		b.Factor = DefaultBackoff.Factor
	}
	return b
}

func (b Backoff) next(d time.Duration) time.Duration {
	nd := time.Duration(float64(d) * b.Factor)
	nd = min(nd, b.Max)
	nd = max(nd, b.Initial)
	return nd
}

// Subscriber keeps a single events.subscribe connection open. Each inbound
// frame — the initial subscription_started and every event thereafter — emits
// one coalesced wakeup on Wakeups. Events only wake; the engine re-reads the
// snapshot for truth. The subscriber reconnects with bounded backoff and emits
// a wakeup on every (re)connection so the engine resyncs after any gap.
type Subscriber struct {
	client   *Client
	subs     []Subscription
	backoff  Backoff
	maxBytes int
	wake     chan struct{}

	// stableAfter is the minimum post-start lifetime that qualifies a
	// subscription as stable (backoff-resetting) even if it delivered no event.
	stableAfter time.Duration
}

// Subscribe builds a [Subscriber] for the given subscriptions. Pass
// [LifecycleSubscriptions] for the state engine.
func (c *Client) Subscribe(subs []Subscription, backoff Backoff) *Subscriber {
	return &Subscriber{
		client:      c,
		subs:        subs,
		backoff:     backoff.normalized(),
		maxBytes:    c.maxBytes,
		wake:        make(chan struct{}, 1),
		stableAfter: DefaultStableSubscription,
	}
}

// Wakeups delivers a coalesced signal whenever Herdr reports activity. A slow
// consumer never blocks the subscriber: extra signals collapse into the one
// buffered slot.
func (s *Subscriber) Wakeups() <-chan struct{} { return s.wake }

func (s *Subscriber) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run maintains the subscription until ctx is canceled, reconnecting with
// bounded backoff. It returns ctx.Err() on cancellation.
//
// Backoff is reset only after a session proves itself — one that stays up past
// stableAfter or delivers at least one real post-start event. A server that
// accepts the subscription and immediately drops it therefore backs off
// exponentially instead of hammering reconnects (and the engine) every Initial.
func (s *Subscriber) Run(ctx context.Context) error {
	delay := s.backoff.Initial
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		stable := s.session(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if stable {
			delay = s.backoff.Initial
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.client.clock.After(delay):
		}
		if !stable {
			delay = s.backoff.next(delay)
		}
	}
}

// session runs one connection lifetime. It returns true only when the session
// was stable — it either delivered a real post-start event or stayed up past
// stableAfter — so Run resets backoff only for a genuinely healthy connection,
// never for an accept-then-drop flap.
func (s *Subscriber) session(ctx context.Context) (stable bool) {
	conn, err := s.client.dialer.Dial(ctx)
	if err != nil {
		return false
	}
	defer conn.Close()

	// Close the connection when ctx is canceled so blocking reads unwind.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	reqBytes, err := json.Marshal(request{
		ID:     s.client.nextID(),
		Method: "events.subscribe",
		Params: eventsSubscribeParams{Subscriptions: s.subs},
	})
	if err != nil {
		return false
	}
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return false
	}

	r := bufio.NewReader(conn)
	first, err := readFrame(r, s.maxBytes)
	if err != nil {
		return false
	}
	if !frameIsSubscriptionStarted(first) {
		return false
	}
	startedAt := s.client.clock.Now()
	s.signal() // resync poll on (re)connect

	gotEvent := false
	for {
		if _, err := readFrame(r, s.maxBytes); err != nil {
			if gotEvent {
				return true
			}
			return s.client.clock.Now().Sub(startedAt) >= s.stableAfter
		}
		gotEvent = true
		s.signal()
	}
}

func frameIsSubscriptionStarted(frame []byte) bool {
	var env envelope
	if err := json.Unmarshal(frame, &env); err != nil || env.Error != nil || len(env.Result) == 0 {
		return false
	}
	var rt resultType
	if err := json.Unmarshal(env.Result, &rt); err != nil {
		return false
	}
	return rt.Type == "subscription_started"
}
