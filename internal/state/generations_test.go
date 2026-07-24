package state

import (
	"testing"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

func TestGenerationBumpsOnOccupantChange(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	if g, ok := e.Generation("w1:p1"); !ok || g != 1 {
		t.Fatalf("initial generation = %d,%v want 1,true", g, ok)
	}

	// Same occupant across polls: generation stable.
	e.Wake()
	waitFor(t, "poll ran", func() bool { return src.callCount() >= 2 })
	if g, _ := e.Generation("w1:p1"); g != 1 {
		t.Fatalf("generation changed without an occupant change: %d", g)
	}

	// New agent session in the same pane id: generation increments.
	src.set(snapshotWith(herdr.StatusWorking, pane("w1:p1", "claude", "s2", herdr.StatusWorking)), nil)
	e.Wake()
	waitFor(t, "gen 2", func() bool { g, _ := e.Generation("w1:p1"); return g == 2 })

	// A stale expected generation must fail the guard; the fresh one passes.
	if e.CheckGeneration("w1:p1", 1) {
		t.Fatal("stale generation 1 must not pass the guard")
	}
	if !e.CheckGeneration("w1:p1", 2) {
		t.Fatal("current generation 2 must pass the guard")
	}
}

func TestGenerationDroppedWhenPaneGone(t *testing.T) {
	t.Parallel()
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	// Pane exits/closes: it disappears from the snapshot.
	src.set(snapshotWith(herdr.StatusIdle), nil)
	e.Wake()
	waitFor(t, "pane gone", func() bool { _, ok := e.Generation("w1:p1"); return !ok })

	if _, ok := e.Generation("w1:p1"); ok {
		t.Fatal("gone pane must have no generation")
	}
	// A mutation still holding the old id is rejected.
	if e.CheckGeneration("w1:p1", 1) {
		t.Fatal("guard must reject a gone pane")
	}
}

func TestGenerationMoveToNewID(t *testing.T) {
	t.Parallel()
	// A cross-workspace move changes the pane id: the old id vanishes and the
	// new id starts fresh at generation 1.
	src := newSource(snapshotWith(herdr.StatusIdle, pane("w1:p1", "claude", "s1", herdr.StatusIdle)))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	moved := pane("w2:p1", "claude", "s1", herdr.StatusIdle)
	moved.WorkspaceID = "w2"
	moved.TabID = "w2:t1"
	next := &herdr.Snapshot{
		Version:  "0.7.5",
		Protocol: 17,
		Workspaces: []herdr.Workspace{
			{WorkspaceID: "w2", Number: 1, Label: "w2", AgentStatus: herdr.StatusIdle, ActiveTabID: "w2:t1"},
		},
		Tabs:  []herdr.Tab{{TabID: "w2:t1", WorkspaceID: "w2", Number: 1, AgentStatus: herdr.StatusIdle}},
		Panes: []herdr.Pane{moved},
	}
	src.set(next, nil)
	e.Wake()

	waitFor(t, "new id present", func() bool { _, ok := e.Generation("w2:p1"); return ok })
	if g, ok := e.Generation("w2:p1"); !ok || g != 1 {
		t.Fatalf("moved pane new id generation = %d,%v want 1,true", g, ok)
	}
	if _, ok := e.Generation("w1:p1"); ok {
		t.Fatal("old pane id must no longer resolve after a move to a new id")
	}
}
