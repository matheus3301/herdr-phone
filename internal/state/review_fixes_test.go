package state

import (
	"slices"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// M8 — identical topology in a different wire order must hash the same, so the
// engine does not rebroadcast every poll.
func TestHashStableUnderReordering(t *testing.T) {
	t.Parallel()
	base := &herdr.Snapshot{
		Version: "0.7.5", Protocol: 17,
		FocusedWorkspaceID: "w1", FocusedTabID: "w1:t1", FocusedPaneID: "w1:p1",
		Workspaces: []herdr.Workspace{
			{WorkspaceID: "w1", Number: 1, Label: "a", AgentStatus: herdr.StatusIdle, ActiveTabID: "w1:t1",
				Worktree: &herdr.WorkspaceWorktree{
					RepoKey: "k1", RepoName: "a", RepoRoot: "/r/a", CheckoutPath: "/r/a", IsLinkedWorktree: true,
				}},
			{WorkspaceID: "w2", Number: 2, Label: "b", AgentStatus: herdr.StatusWorking, ActiveTabID: "w2:t1"},
		},
		Tabs: []herdr.Tab{
			{TabID: "w1:t1", WorkspaceID: "w1", Number: 1, AgentStatus: herdr.StatusIdle},
			{TabID: "w2:t1", WorkspaceID: "w2", Number: 1, AgentStatus: herdr.StatusWorking},
		},
		Panes: []herdr.Pane{
			{PaneID: "w1:p1", TerminalID: "t1", WorkspaceID: "w1", TabID: "w1:t1", Agent: "claude", AgentStatus: herdr.StatusIdle},
			{PaneID: "w2:p1", TerminalID: "t2", WorkspaceID: "w2", TabID: "w2:t1", Agent: "codex", AgentStatus: herdr.StatusWorking},
		},
		Agents: []herdr.Agent{
			{PaneID: "w1:p1", Agent: "claude", AgentStatus: herdr.StatusIdle, WorkspaceID: "w1", TabID: "w1:t1"},
			{PaneID: "w2:p1", Agent: "codex", AgentStatus: herdr.StatusWorking, WorkspaceID: "w2", TabID: "w2:t1"},
		},
		Layouts: []herdr.Layout{
			{WorkspaceID: "w1", TabID: "w1:t1", FocusedPaneID: "w1:p1",
				Panes:  []herdr.LayoutPane{{PaneID: "w1:p1"}, {PaneID: "w1:p2"}},
				Splits: []herdr.LayoutSplit{{ID: "s0"}, {ID: "s1"}}},
			{WorkspaceID: "w2", TabID: "w2:t1", FocusedPaneID: "w2:p1"},
		},
	}

	reordered := &herdr.Snapshot{
		Version: "0.7.5", Protocol: 17,
		FocusedWorkspaceID: "w1", FocusedTabID: "w1:t1", FocusedPaneID: "w1:p1",
		Workspaces: reverse(base.Workspaces),
		Tabs:       reverse(base.Tabs),
		Panes:      reverse(base.Panes),
		Agents:     reverse(base.Agents),
		Layouts:    reverse(cloneLayoutsReversedInner(base.Layouts)),
	}

	if hashTopology(base) != hashTopology(reordered) {
		t.Fatalf("hash changed under pure reordering:\n base=%s\n reord=%s",
			hashTopology(base), hashTopology(reordered))
	}

	// A genuinely meaningful change must still change the hash.
	changed := *base
	changed.Panes = slices.Clone(base.Panes)
	changed.Panes[0].AgentStatus = herdr.StatusBlocked
	if hashTopology(base) == hashTopology(&changed) {
		t.Fatal("hash did not change on a real status change")
	}

	// hashing must not mutate the caller's slices (order preserved).
	if base.Workspaces[0].WorkspaceID != "w1" || base.Panes[0].PaneID != "w1:p1" {
		t.Fatal("project mutated the input snapshot order")
	}
}

func reverse[T any](in []T) []T {
	out := slices.Clone(in)
	slices.Reverse(out)
	return out
}

func cloneLayoutsReversedInner(in []herdr.Layout) []herdr.Layout {
	out := slices.Clone(in)
	for i := range out {
		out[i].Panes = reverse(out[i].Panes)
		out[i].Splits = reverse(out[i].Splits)
	}
	return out
}

// M9 — a working pane under an idle workspace rollup must still force the hot
// cadence.
func TestHotCadenceFromWorkingPaneIdleWorkspace(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusWorking)))
	e, clock := startEngine(t, Config{Source: src, PollHot: 300 * time.Millisecond, PollCold: 9 * time.Second})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })
	waitFor(t, "hot from working pane", func() bool { return clock.interval() == 300*time.Millisecond })
}

// M9 — a working agent under an idle workspace/idle pane must also force hot.
func TestHotCadenceFromWorkingAgentIdleWorkspace(t *testing.T) {
	t.Parallel()
	snap := &herdr.Snapshot{
		Version: "0.7.5", Protocol: 17,
		Workspaces: []herdr.Workspace{{WorkspaceID: "w1", Number: 1, AgentStatus: herdr.StatusIdle, ActiveTabID: "w1:t1"}},
		Tabs:       []herdr.Tab{{TabID: "w1:t1", WorkspaceID: "w1", AgentStatus: herdr.StatusIdle}},
		Panes:      []herdr.Pane{{PaneID: "w1:p1", TerminalID: "t1", WorkspaceID: "w1", TabID: "w1:t1", Agent: "claude", AgentStatus: herdr.StatusIdle}},
		Agents:     []herdr.Agent{{PaneID: "w1:p1", Agent: "claude", AgentStatus: herdr.StatusWorking, WorkspaceID: "w1", TabID: "w1:t1"}},
	}
	src := newSource(snap)
	e, clock := startEngine(t, Config{Source: src, PollHot: 300 * time.Millisecond, PollCold: 9 * time.Second})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })
	waitFor(t, "hot from working agent", func() bool { return clock.interval() == 300*time.Millisecond })
}

// M9 — fully idle with no subscribers stays cold (guards against over-eager hot).
func TestColdCadenceWhenFullyIdle(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	e, clock := startEngine(t, Config{Source: src, PollHot: 300 * time.Millisecond, PollCold: 9 * time.Second})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })
	waitFor(t, "cold when idle", func() bool { return clock.interval() == 9*time.Second })
}

// L18 — a seed that exceeds the client's byte budget overflows through enqueue
// and the subscription is not registered (no silent bypass of the bound).
func TestSeedOverflowNotRegistered(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	e, _ := startEngine(t, Config{Source: src, MaxQueueBytes: 8})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	sub := e.Subscribe()
	if !sub.Overflow() {
		t.Fatal("oversized seed must overflow the subscription")
	}
	select {
	case <-sub.Done():
	default:
		t.Fatal("overflowed seed must close the subscription")
	}
	if e.Stats().Subscribers != 0 {
		t.Fatalf("overflowed subscription must not be registered: subs=%d", e.Stats().Subscribers)
	}
	// A closed subscription holds nothing.
	if sub.Latest() != nil {
		t.Fatal("closed subscription must not retain the seed")
	}
}

// L18 — a seed within budget is delivered and the subscription is registered.
func TestSeedWithinBudgetRegistered(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	e, _ := startEngine(t, Config{Source: src, MaxQueueBytes: 1 << 20})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	sub := e.Subscribe()
	if sub.Overflow() {
		t.Fatal("in-budget seed must not overflow")
	}
	if got := sub.Latest(); got == nil || got.Seq != 1 {
		t.Fatalf("seed not delivered through enqueue: %+v", got)
	}
	if e.Stats().Subscribers != 1 {
		t.Fatalf("in-budget subscription must be registered: subs=%d", e.Stats().Subscribers)
	}
}
