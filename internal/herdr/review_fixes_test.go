package herdr

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// L8 — HERDR_SOCKET_PATH must be ~-expanded like the configured value.
func TestResolveSocketPathEnvTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_SOCKET_PATH", "~/run/herdr.sock")
	got, err := ResolveSocketPath("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "run", "herdr.sock")
	if got != want {
		t.Fatalf("env tilde not expanded: got %q want %q", got, want)
	}

	// A bare "~" env value expands to the home directory itself.
	t.Setenv("HERDR_SOCKET_PATH", "~")
	got, err = ResolveSocketPath("")
	if err != nil || got != home {
		t.Fatalf("bare ~ env not expanded: got %q err %v", got, err)
	}
}

// L20 — Handshake validates version compatibility as well as protocol, but must
// accept future patch/minor/major releases and never reject on an unparseable
// version.
func TestHandshakeVersionBoundaries(t *testing.T) {
	cases := []struct {
		version    string
		wantReject bool
	}{
		{"0.7.5", false},      // exact minimum
		{"0.7.6", false},      // future patch
		{"0.7.99", false},     // far future patch
		{"0.8.0", false},      // future minor
		{"1.0.0", false},      // future major
		{"0.7.5-rc.1", false}, // pre-release of the minimum: not older
		{"v0.8.0", false},     // leading v tolerated
		{"0.7.4", true},       // older patch
		{"0.6.9", true},       // older minor
		{"0.0.0", true},       // ancient
		{"", false},           // unparseable → defer to protocol gate
		{"garbage", false},    // unparseable → defer to protocol gate
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			s := newServer(func(req map[string]any) []byte {
				return reply(req, map[string]any{"type": "pong", "version": tc.version, "protocol": 17})
			})
			c := newTestClient(t, s)
			_, err := c.Handshake(context.Background())
			if tc.wantReject && !IsCode(err, CodeIncompatible) {
				t.Fatalf("version %q should be rejected, got err=%v", tc.version, err)
			}
			if !tc.wantReject && err != nil {
				t.Fatalf("version %q should be accepted, got err=%v", tc.version, err)
			}
		})
	}
}

// L20 — a bad protocol is still rejected regardless of a fine version.
func TestHandshakeProtocolStillGatesWithGoodVersion(t *testing.T) {
	s := newServer(func(req map[string]any) []byte {
		return reply(req, map[string]any{"type": "pong", "version": "9.9.9", "protocol": 16})
	})
	c := newTestClient(t, s)
	if _, err := c.Handshake(context.Background()); !IsCode(err, CodeIncompatible) {
		t.Fatalf("bad protocol must reject even with a new version: %v", err)
	}
}

// L2 — a server that accepts then immediately drops the subscription must back
// off exponentially, not reset to Initial every cycle.
func TestSubscriberBackoffGrowsOnAcceptThenDrop(t *testing.T) {
	clock := newFakeClock()
	dialer, dials := eventsDialer(func(conn net.Conn, dial int) {
		r := bufio.NewReader(conn)
		readSubscribe(t, r)
		// Accept, then drop immediately with no event and no elapsed time.
		writeFrame(conn, "sub", map[string]any{"type": "subscription_started"})
	})
	c := NewClient(dialer, WithClock(clock))
	sub := c.Subscribe(LifecycleSubscriptions(), Backoff{Initial: 100 * time.Millisecond, Max: 10 * time.Second, Factor: 2})
	go func() { _ = sub.Run(t.Context()) }()

	waitForInt(t, dials, 1)
	for cycle := 2; cycle <= 4; cycle++ {
		waitFor(t, func() bool { return clock.pendingCount() > 0 }) // reconnect timer armed
		clock.fireAfter()
		waitForInt(t, dials, int32(cycle))
	}
	waitFor(t, func() bool { return len(clock.afterDurations()) >= 3 })

	durs := clock.afterDurations()
	// Backoff must be strictly increasing across the flap.
	for i := 1; i < len(durs) && i < 4; i++ {
		if durs[i] <= durs[i-1] {
			t.Fatalf("backoff did not grow on accept-then-drop: %v", durs)
		}
	}
	if durs[0] != 100*time.Millisecond {
		t.Fatalf("first backoff should be Initial, got %v", durs[0])
	}
}

// L2 — a subscription that delivers a real post-start event resets the backoff.
func TestSubscriberBackoffResetsOnRealEvent(t *testing.T) {
	clock := newFakeClock()
	dialer, dials := eventsDialer(func(conn net.Conn, dial int) {
		r := bufio.NewReader(conn)
		readSubscribe(t, r)
		writeFrame(conn, "sub", map[string]any{"type": "subscription_started"})
		// A genuine event: this makes the session productive → backoff resets.
		writeFrame(conn, "", map[string]any{"type": "pane_created", "pane_id": "w1:p9"})
	})
	c := NewClient(dialer, WithClock(clock))
	sub := c.Subscribe(LifecycleSubscriptions(), Backoff{Initial: 100 * time.Millisecond, Max: 10 * time.Second, Factor: 2})
	go func() { _ = sub.Run(t.Context()) }()

	waitForInt(t, dials, 1)
	for cycle := 2; cycle <= 4; cycle++ {
		waitFor(t, func() bool { return clock.pendingCount() > 0 })
		clock.fireAfter()
		waitForInt(t, dials, int32(cycle))
	}
	waitFor(t, func() bool { return len(clock.afterDurations()) >= 3 })

	// Every reconnect delay stays at Initial because each session was productive.
	for i, d := range clock.afterDurations() {
		if d != 100*time.Millisecond {
			t.Fatalf("backoff should stay Initial after real events, delay[%d]=%v", i, d)
		}
	}
}

// L2 — the stable-duration branch: with stableAfter=0 any established session
// (even an eventless accept-then-drop) counts as stable, so backoff never grows.
// This exercises the elapsed >= stableAfter comparison from the true side.
func TestSubscriberStableAfterZeroKeepsBackoffAtInitial(t *testing.T) {
	clock := newFakeClock()
	dialer, dials := eventsDialer(func(conn net.Conn, dial int) {
		r := bufio.NewReader(conn)
		readSubscribe(t, r)
		writeFrame(conn, "sub", map[string]any{"type": "subscription_started"})
		// Drop immediately, no event; only stableAfter=0 makes this "stable".
	})
	c := NewClient(dialer, WithClock(clock))
	sub := c.Subscribe(LifecycleSubscriptions(), Backoff{Initial: 100 * time.Millisecond, Max: 10 * time.Second, Factor: 2})
	sub.stableAfter = 0

	go func() { _ = sub.Run(t.Context()) }()
	waitForInt(t, dials, 1)
	for cycle := 2; cycle <= 3; cycle++ {
		waitFor(t, func() bool { return clock.pendingCount() > 0 })
		clock.fireAfter()
		waitForInt(t, dials, int32(cycle))
	}
	waitFor(t, func() bool { return len(clock.afterDurations()) >= 2 })
	for i, d := range clock.afterDurations() {
		if d != 100*time.Millisecond {
			t.Fatalf("stableAfter=0 must keep backoff at Initial, delay[%d]=%v", i, d)
		}
	}
}

// waitForInt spins until *v >= want.
func waitForInt(t *testing.T, v *int32, want int32) {
	t.Helper()
	waitFor(t, func() bool { return atomic.LoadInt32(v) >= want })
}
