package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// eventsDialer runs a per-connection script so tests fully control the
// events.subscribe stream and connection lifetime.
func eventsDialer(script func(conn net.Conn, dial int)) (Dialer, *int32) {
	var dials int32
	d := DialerFunc(func(ctx context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		n := atomic.AddInt32(&dials, 1)
		go func() {
			defer server.Close()
			script(server, int(n))
		}()
		return client, nil
	})
	return d, &dials
}

// readSubscribe consumes the subscribe request line.
func readSubscribe(t *testing.T, r *bufio.Reader) {
	t.Helper()
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req map[string]any
	if err := json.Unmarshal(line, &req); err != nil {
		t.Errorf("bad subscribe request: %v", err)
	}
	if req["method"] != "events.subscribe" {
		t.Errorf("method = %v want events.subscribe", req["method"])
	}
}

func writeFrame(conn net.Conn, id string, result map[string]any) {
	env := map[string]any{"id": id, "result": result}
	b, _ := json.Marshal(env)
	conn.Write(append(b, '\n'))
}

func TestSubscriberWakesAndReconnects(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()

	dialer, dials := eventsDialer(func(conn net.Conn, dial int) {
		r := bufio.NewReader(conn)
		readSubscribe(t, r)
		writeFrame(conn, "sub", map[string]any{"type": "subscription_started"})
		if dial == 1 {
			// One event then drop the connection to force a reconnect.
			writeFrame(conn, "", map[string]any{"type": "pane_created", "pane_id": "w1:p9"})
			return
		}
		// Second connection stays open, blocking on read until ctx closes it.
		_, _ = r.ReadBytes('\n')
	})

	c := NewClient(dialer, WithClock(clock))
	sub := c.Subscribe(LifecycleSubscriptions(), Backoff{Initial: time.Second, Max: time.Second, Factor: 2})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { _ = sub.Run(ctx); close(runDone) }()

	// First session establishes and delivers a wakeup.
	select {
	case <-sub.Wakeups():
	case <-time.After(2 * time.Second):
		t.Fatal("no wakeup from first session")
	}

	// After the first session drops, Run backs off on the clock. Fire it to
	// trigger the reconnect.
	waitFor(t, func() bool { return clock.pendingCount() > 0 })
	clock.fireAfter()
	waitFor(t, func() bool { return atomic.LoadInt32(dials) >= 2 })

	// The reconnect emits a resync wakeup too.
	select {
	case <-sub.Wakeups():
	case <-time.After(2 * time.Second):
		t.Fatal("no wakeup after reconnect")
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestSubscriberBacksOffWhenSubscriptionRejected(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	dialer, dials := eventsDialer(func(conn net.Conn, dial int) {
		r := bufio.NewReader(conn)
		readSubscribe(t, r)
		// Reject: send an error instead of subscription_started.
		env := map[string]any{"id": "sub", "error": map[string]any{"code": "feature_disabled", "message": "no"}}
		b, _ := json.Marshal(env)
		conn.Write(append(b, '\n'))
	})
	c := NewClient(dialer, WithClock(clock))
	sub := c.Subscribe(LifecycleSubscriptions(), Backoff{Initial: 100 * time.Millisecond, Max: 4 * time.Second, Factor: 2})
	go func() { _ = sub.Run(t.Context()) }()

	// No wakeup should arrive (never established); reconnects keep happening as
	// we fire the backoff timer.
	waitFor(t, func() bool { return atomic.LoadInt32(dials) >= 1 })
	waitFor(t, func() bool { return clock.pendingCount() > 0 })
	clock.fireAfter()
	waitFor(t, func() bool { return atomic.LoadInt32(dials) >= 2 })

	select {
	case <-sub.Wakeups():
		t.Fatal("unexpected wakeup: subscription never established")
	default:
	}
}

func TestLifecycleSubscriptionsAreSessionWide(t *testing.T) {
	t.Parallel()
	subs := LifecycleSubscriptions()
	if len(subs) == 0 {
		t.Fatal("no lifecycle subscriptions")
	}
	for _, s := range subs {
		if s.Type == "" {
			t.Fatal("subscription with empty type")
		}
		// Session-wide subscriptions must not carry a scope id.
		if s.PaneID != "" || s.TabID != "" || s.WorkspaceID != "" {
			t.Fatalf("lifecycle subscription %q must be session-wide", s.Type)
		}
	}
}
