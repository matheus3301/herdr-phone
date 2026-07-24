package state

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	for range 200_000 {
		if cond() {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("condition not met: %s", msg)
}

func startEngine(t *testing.T, cfg Config) (*Engine, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	cfg.Clock = clock
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = e.Run(t.Context()) }()
	return e, clock
}

func TestNewValidatesAndClamps(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("Source is required")
	}
	src := newSource(snapshotWith(herdr.StatusIdle))
	e, err := New(Config{Source: src, PollHot: 10 * time.Millisecond, PollCold: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if e.pollHot != MinPollHot {
		t.Fatalf("hot not clamped to min: %v", e.pollHot)
	}
	if e.pollCold < e.pollHot {
		t.Fatalf("cold must be >= hot: cold=%v hot=%v", e.pollCold, e.pollHot)
	}
}

func TestInitialPollPopulatesSnapshot(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "first poll", func() bool { return e.Current() != nil })
	cur := e.Current()
	if cur.Seq != 1 || cur.Topology == nil || len(cur.Topology.Panes) != 1 {
		t.Fatalf("bad initial snapshot: %+v", cur)
	}
	if g, ok := e.Generation("w1:p1"); !ok || g != 1 {
		t.Fatalf("pane generation = %d,%v want 1,true", g, ok)
	}
}

func TestBroadcastOnlyOnChange(t *testing.T) {
	t.Parallel()
	a := snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle))
	src := newSource(a)
	e, clock := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	// Same content across several completed polls: no new broadcast. Drive each
	// poll via the ticker and wait for it to finish, so the count is
	// deterministic (Wake would coalesce and not guarantee a fixed poll count).
	for range 3 {
		n := src.callCount()
		clock.tick()
		waitFor(t, "poll completed", func() bool { return src.callCount() > n })
	}
	if seq := e.Stats().Seq; seq != 1 {
		t.Fatalf("unchanged topology rebroadcast: seq=%d", seq)
	}

	// Meaningful change: new agent status.
	src.set(snapshotWith(herdr.StatusWorking, pane("w1:p1", "claude", "s1", herdr.StatusWorking)), nil)
	e.Wake()
	waitFor(t, "seq 2", func() bool { return e.Stats().Seq == 2 })
}

func TestVolatileRevisionDoesNotRebroadcast(t *testing.T) {
	t.Parallel()
	base := snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle))
	base.Panes[0].Revision = 1
	src := newSource(base)
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	// Only the revision/scroll changed (terminal output). No rebroadcast.
	next := snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle))
	next.Panes[0].Revision = 999
	next.Panes[0].Scroll = &herdr.Scroll{OffsetFromBottom: 42}
	src.set(next, nil)
	e.Wake()
	waitFor(t, "poll ran", func() bool { return src.callCount() >= 2 })
	if seq := e.Stats().Seq; seq != 1 {
		t.Fatalf("revision-only change rebroadcast: seq=%d", seq)
	}
}

func TestPollOverlapQueuesExactlyOne(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	gate := src.enableGate()
	e, _ := startEngine(t, Config{Source: src})

	// First (startup) poll enters and blocks on the gate.
	<-src.entered
	if src.callCount() != 1 {
		t.Fatalf("expected 1 in-flight poll, got %d", src.callCount())
	}

	// Fire a burst of wakeups while a poll is in progress, and wait until they
	// have all been folded into the single queued follow-up (the wake channel
	// drains to empty). Because the only in-flight poll is gated, the loop can
	// only be servicing wakes here, so an empty channel means queue depth 1.
	for range 5 {
		e.Wake()
	}
	waitFor(t, "wakes folded into one queued follow-up", func() bool { return len(e.wake) == 0 })

	// While the poll is gated, no concurrent poll may have been spawned.
	if c := src.callCount(); c != 1 {
		t.Fatalf("wakeups during an in-flight poll must not spawn a concurrent poll: calls=%d", c)
	}

	// Release poll 1; exactly the one queued follow-up runs.
	gate <- struct{}{}
	<-src.entered // poll 2 enters
	gate <- struct{}{}

	waitFor(t, "poll 2 completed", func() bool { return e.Stats().Polls >= 2 })
	// With no further wakeups or ticks, no third poll may run.
	for range 500 {
		runtime.Gosched()
	}
	if c := src.callCount(); c != 2 {
		t.Fatalf("burst of 5 wakeups during a poll must queue exactly one follow-up: calls=%d", c)
	}
}

func TestHotColdCadence(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "", "", herdr.StatusIdle)))
	e, clock := startEngine(t, Config{Source: src, PollHot: 300 * time.Millisecond, PollCold: 9 * time.Second})

	// Idle, no subscribers -> cold.
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })
	waitFor(t, "cold interval", func() bool { return clock.interval() == 9*time.Second })

	// A subscribed browser forces hot even while idle.
	sub := e.Subscribe()
	e.Wake()
	waitFor(t, "hot from subscriber", func() bool { return clock.interval() == 300*time.Millisecond })
	e.Unsubscribe(sub)

	// Back to idle with no subscribers -> cold again.
	e.Wake()
	waitFor(t, "cold again", func() bool { return clock.interval() == 9*time.Second })

	// A working agent forces hot regardless of subscribers.
	src.set(snapshotWith(herdr.StatusWorking, pane("w1:p1", "claude", "s1", herdr.StatusWorking)), nil)
	e.Wake()
	waitFor(t, "hot from working agent", func() bool { return clock.interval() == 300*time.Millisecond })
}

func TestTickerAlsoPolls(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	_, clock := startEngine(t, Config{Source: src})
	waitFor(t, "startup poll", func() bool { return src.callCount() >= 1 })
	before := src.callCount()
	clock.tick()
	waitFor(t, "tick caused a poll", func() bool { return src.callCount() > before })
}

func TestPollErrorKeepsLastGoodSnapshot(t *testing.T) {
	t.Parallel()
	good := snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle))
	src := newSource(good)
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	src.set(nil, errors.New("herdr unreachable"))
	e.Wake()
	waitFor(t, "error recorded", func() bool { return e.Stats().LastPollErr != nil })

	cur := e.Current()
	if cur == nil || cur.Seq != 1 || len(cur.Topology.Panes) != 1 {
		t.Fatalf("transient error discarded last good snapshot: %+v", cur)
	}
}

func TestSubscriberReceivesSnapshotsAndCoalesces(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	sub := e.Subscribe()
	// Seeded with current.
	if got := sub.Latest(); got == nil || got.Seq != 1 {
		t.Fatalf("subscriber not seeded with current snapshot: %+v", got)
	}

	// Drive several distinct changes without draining; they must coalesce to
	// the newest.
	for i, st := range []herdr.AgentStatus{herdr.StatusWorking, herdr.StatusIdle, herdr.StatusBlocked} {
		agent := "claude"
		src.set(snapshotWith(st, pane("w1:p1", agent, "s1", st)), nil)
		e.Wake()
		waitFor(t, "broadcast", func() bool { return e.Stats().Seq >= uint64(2+i) })
	}
	drained := sub.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected coalesced single snapshot, got %d", len(drained))
	}
	if drained[0].Seq != e.Stats().Seq {
		t.Fatalf("coalesced snapshot is not the newest: got seq %d want %d", drained[0].Seq, e.Stats().Seq)
	}
}

func TestSlowSubscriberOverflowIsDropped(t *testing.T) {
	t.Parallel()
	// A budget that comfortably holds a one-pane seed but not a large snapshot,
	// so overflow is exercised on a broadcast (not the seed).
	small := snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle))
	src := newSource(small)
	e, _ := startEngine(t, Config{Source: src, MaxQueueBytes: 4000})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	sub := e.Subscribe()
	if sub.Overflow() {
		t.Fatal("small seed should fit the budget")
	}
	sub.Drain() // consume the seed

	// Broadcast a much larger snapshot that exceeds the byte budget.
	big := manyPaneSnapshot(60, herdr.StatusWorking)
	src.set(big, nil)
	e.Wake()

	select {
	case <-sub.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("overflowing subscriber was not closed")
	}
	if !sub.Overflow() {
		t.Fatal("subscriber should report overflow on the oversized broadcast")
	}
	waitFor(t, "engine dropped subscriber", func() bool { return e.Stats().Subscribers == 0 })
}

// manyPaneSnapshot builds a snapshot with n panes to exceed a small byte budget.
func manyPaneSnapshot(n int, status herdr.AgentStatus) *herdr.Snapshot {
	s := &herdr.Snapshot{
		Version: "0.7.5", Protocol: 17,
		Workspaces: []herdr.Workspace{{WorkspaceID: "w1", Number: 1, AgentStatus: status, ActiveTabID: "w1:t1"}},
		Tabs:       []herdr.Tab{{TabID: "w1:t1", WorkspaceID: "w1", AgentStatus: status}},
	}
	for i := range n {
		id := "w1:p" + itoaSmall(i)
		s.Panes = append(s.Panes, herdr.Pane{
			PaneID: id, TerminalID: "term_" + id, WorkspaceID: "w1", TabID: "w1:t1",
			Agent: "claude", AgentStatus: status, CWD: "/Users/someone/work/project/subdir",
			TerminalTitle: "a reasonably long terminal title to add bytes per pane",
		})
	}
	return s
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
