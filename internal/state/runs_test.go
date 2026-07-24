package state

import (
	"reflect"
	"slices"
	"testing"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// runSnapshot builds a topology with one agent pane, its agent-list entry, and a
// workspace carrying git checkout provenance.
func runSnapshot(status herdr.AgentStatus) *herdr.Snapshot {
	p := pane("w1:p1", "claude", "s1", status)
	p.Title = "Fix auth refresh"
	p.DisplayAgent = "Claude Code"
	p.CWD = "/code/space-api"
	p.ForegroundCWD = "/code/space-api"
	p.Revision = 42
	return &herdr.Snapshot{
		Version:  "0.7.5",
		Protocol: 17,
		Workspaces: []herdr.Workspace{{
			WorkspaceID: "w1", Number: 1, Label: "space-api", AgentStatus: status, ActiveTabID: "w1:t1",
			Worktree: &herdr.WorkspaceWorktree{
				RepoKey: "k1", RepoName: "space-api", RepoRoot: "/code/space-api",
				CheckoutPath: "/code/space-api-auth", IsLinkedWorktree: true,
			},
		}},
		Tabs:  []herdr.Tab{{TabID: "w1:t1", WorkspaceID: "w1", Number: 1, Label: "agents", AgentStatus: status}},
		Panes: []herdr.Pane{p},
		Agents: []herdr.Agent{{
			TerminalID: "term_w1:p1", Agent: "claude", Name: "auth", PaneID: "w1:p1",
			WorkspaceID: "w1", TabID: "w1:t1", AgentStatus: status,
			InteractiveReady: true, StateChangeSeq: 9,
		}},
	}
}

func TestRunsProjectIdentityAndContext(t *testing.T) {
	t.Parallel()
	src := newSource(runSnapshot(herdr.StatusBlocked))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	set := e.Runs()
	if set.SnapshotHash == "" {
		t.Error("projection must carry the snapshot hash it came from")
	}
	runs := set.Runs
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	r := runs[0]
	if r.PaneID != "w1:p1" || r.PaneGeneration != 1 {
		t.Errorf("pane identity = %s@%d, want w1:p1@1", r.PaneID, r.PaneGeneration)
	}
	if len(r.AgentIncarnation) != incarnationDigestLen {
		t.Errorf("incarnation = %q, want %d hex chars", r.AgentIncarnation, incarnationDigestLen)
	}
	// The opaque handle binds pane, generation, AND incarnation, so a recycled
	// pane id that restarts at generation 1 cannot reuse a dead run's identity.
	if want := "w1:p1@1#" + r.AgentIncarnation; r.RunID != want {
		t.Errorf("run id = %q, want %q", r.RunID, want)
	}
	if r.WorkspaceID != "w1" || r.WorkspaceLabel != "space-api" || r.TabID != "w1:t1" || r.TabLabel != "agents" {
		t.Errorf("topology context = %+v", r)
	}
	if r.AgentKind != "claude" || r.AgentName != "auth" || r.DisplayAgent != "Claude Code" {
		t.Errorf("agent identity = kind=%q name=%q display=%q", r.AgentKind, r.AgentName, r.DisplayAgent)
	}
	if r.Title != "Fix auth refresh" {
		t.Errorf("title = %q", r.Title)
	}
	if r.Status != herdr.StatusBlocked {
		t.Errorf("status = %q, want blocked", r.Status)
	}
	if !r.InteractiveReady || r.StateChangeSeq != 9 || r.Revision != 42 {
		t.Errorf("agent-list facts not carried: %+v", r)
	}
	if r.Worktree == nil {
		t.Fatal("worktree context missing")
	}
	if r.Worktree.RepoName != "space-api" || r.Worktree.CheckoutPath != "/code/space-api-auth" || !r.Worktree.IsLinkedWorktree {
		t.Errorf("worktree = %+v", *r.Worktree)
	}
}

// A run's identity must move with the pane's lifecycle generation, so a client
// holding the old run can be told to reopen rather than silently rebound.
func TestRunIdentityChangesWithGeneration(t *testing.T) {
	t.Parallel()
	src := newSource(runSnapshot(herdr.StatusWorking))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	before := e.Runs().Runs[0]

	// A new agent session in the same pane id: a different incarnation.
	next := runSnapshot(herdr.StatusWorking)
	next.Panes[0].AgentSession = &herdr.AgentSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "s2"}
	src.set(next, nil)
	e.Wake()
	waitFor(t, "gen 2", func() bool { g, _ := e.Generation("w1:p1"); return g == 2 })

	after := e.Runs().Runs[0]
	if after.PaneGeneration != 2 {
		t.Fatalf("generation = %d, want 2", after.PaneGeneration)
	}
	if after.RunID == before.RunID {
		t.Error("run id must change when the pane generation changes")
	}
	if after.AgentIncarnation == before.AgentIncarnation {
		t.Error("agent incarnation must change when the occupant changes")
	}
}

// Review LOW 3: a pane id that vanishes and later reappears restarts at
// generation 1, because updateGenerationsLocked drops the departed pane's entry.
// The opaque run id must still differ, or anything keyed on it (a React key, a
// client-side run partition holding instruction history) would let the new
// occupant inherit the dead run's identity and content.
func TestRunIDIsNotReusedAcrossPaneRecycling(t *testing.T) {
	t.Parallel()
	src := newSource(runSnapshot(herdr.StatusIdle))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })
	before := e.Runs().Runs[0]
	if before.PaneGeneration != 1 {
		t.Fatalf("generation = %d, want 1", before.PaneGeneration)
	}

	// The pane goes away entirely: its generation entry is dropped.
	empty := runSnapshot(herdr.StatusIdle)
	empty.Panes = nil
	empty.Agents = nil
	src.set(empty, nil)
	e.Wake()
	waitFor(t, "pane gone", func() bool { _, ok := e.Generation("w1:p1"); return !ok })

	// The same pane id comes back with a different occupant, so its generation
	// restarts at 1 while the incarnation differs.
	next := runSnapshot(herdr.StatusIdle)
	next.Panes[0].AgentSession = &herdr.AgentSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "s2"}
	src.set(next, nil)
	e.Wake()
	waitFor(t, "pane back", func() bool { g, ok := e.Generation("w1:p1"); return ok && g == 1 })

	after := e.Runs().Runs[0]
	if after.PaneGeneration != 1 {
		t.Fatalf("generation = %d, want 1 (recycled ids restart)", after.PaneGeneration)
	}
	if after.AgentIncarnation == before.AgentIncarnation {
		t.Fatal("incarnation must differ for a different occupant")
	}
	if after.RunID == before.RunID {
		t.Fatalf("run id %q reused across pane recycling", after.RunID)
	}
}

// The projection must never expose a pane the mutation guard cannot check, and
// must never present an empty shell as a run.
func TestRunsExcludeShellPanesAndUngeneratedPanes(t *testing.T) {
	t.Parallel()
	snap := runSnapshot(herdr.StatusIdle)
	shell := pane("w1:p2", "", "", "")
	snap.Panes = append(snap.Panes, shell)
	src := newSource(snap)
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })

	runs := e.Runs().Runs
	if len(runs) != 1 || runs[0].PaneID != "w1:p1" {
		t.Fatalf("runs = %+v, want only the agent pane", runs)
	}

	// A pane present in the topology but absent from the generation map cannot be
	// guarded, so projectRuns must drop it.
	direct := projectRuns(snap, map[string]uint64{}, map[string]string{})
	if len(direct) != 0 {
		t.Fatalf("runs without a live generation = %d, want 0", len(direct))
	}
}

// Status is a closed set: an upstream value outside Herdr's five states must read
// as unknown, never as completion.
func TestRunStatusIsClosedSet(t *testing.T) {
	t.Parallel()
	snap := runSnapshot(herdr.AgentStatus("brand_new_state"))
	snap.Agents[0].AgentStatus = herdr.AgentStatus("brand_new_state")
	runs := projectRuns(snap, map[string]uint64{"w1:p1": 3}, map[string]string{"w1:p1": "fp"})
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Status != herdr.StatusUnknown {
		t.Fatalf("status = %q, want unknown", runs[0].Status)
	}
}

func TestRunsAreOrderedByPaneID(t *testing.T) {
	t.Parallel()
	snap := runSnapshot(herdr.StatusIdle)
	for _, id := range []string{"w1:p9", "w1:p3"} {
		p := pane(id, "codex", "s-"+id, herdr.StatusIdle)
		snap.Panes = append(snap.Panes, p)
	}
	gens := map[string]uint64{"w1:p1": 1, "w1:p3": 1, "w1:p9": 1}
	occ := map[string]string{"w1:p1": "a", "w1:p3": "b", "w1:p9": "c"}
	runs := projectRuns(snap, gens, occ)
	got := make([]string, len(runs))
	for i, r := range runs {
		got[i] = r.PaneID
	}
	want := []string{"w1:p1", "w1:p3", "w1:p9"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// runShape is the exact field set of Run. The run projection is polled and is
// embedded in the run inbox, so a body-bearing field must never be added to it
// without a deliberate review. This canary makes such an addition fail loudly
// rather than silently shipping output in a topology-shaped response.
var runShape = []string{
	"RunID", "PaneID", "PaneGeneration", "AgentIncarnation",
	"WorkspaceID", "WorkspaceLabel", "TabID", "TabLabel", "TerminalID",
	"AgentKind", "AgentName", "DisplayAgent", "Title", "Status",
	"InteractiveReady", "LaunchPending", "Focused",
	"CWD", "ForegroundCWD", "Worktree",
	"Revision", "StateChangeSeq",
}

func TestRunProjectionCarriesNoOutput(t *testing.T) {
	t.Parallel()
	rt := reflect.TypeOf(Run{})
	got := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		got = append(got, rt.Field(i).Name)
	}
	if !slices.Equal(got, runShape) {
		t.Fatalf("Run field set changed:\n got %v\nwant %v\n"+
			"a new field must be reviewed for output/transcript content before being added", got, runShape)
	}
}

func TestRunsNilBeforeFirstPoll(t *testing.T) {
	t.Parallel()
	e, err := New(Config{Source: newSource(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if set := e.Runs(); set.Runs != nil || set.SnapshotHash != "" {
		t.Fatalf("projection before first poll = %+v, want zero", set)
	}
}

// The incarnation must be a digest, never the raw occupant fingerprint, because
// the fingerprint embeds an agent session reference that may be a filesystem path.
func TestIncarnationHidesSessionReference(t *testing.T) {
	t.Parallel()
	fp := "term_w1:p1\x00claude\x00herdr:claude\x00path\x00/Users/op/.claude/sessions/abc.json"
	got := incarnation(fp)
	if got == "" || len(got) != incarnationDigestLen {
		t.Fatalf("incarnation = %q", got)
	}
	if got == fp {
		t.Fatal("incarnation must not be the raw fingerprint")
	}
	if incarnation("") != "" {
		t.Fatal("an absent fingerprint must digest to the empty string")
	}
	if incarnation(fp) != got {
		t.Fatal("incarnation must be stable")
	}
	if incarnation(fp+"x") == got {
		t.Fatal("incarnation must change with the fingerprint")
	}
}

// The run contract adds no event of its own: a client learns a run moved from the
// existing topology broadcast and refetches. So a status change must bump the seq
// and reach the projection — snapshot stays truth, events stay wakeups.
func TestRunStatusChangeRebroadcasts(t *testing.T) {
	t.Parallel()
	src := newSource(runSnapshot(herdr.StatusWorking))
	e, _ := startEngine(t, Config{Source: src})
	waitFor(t, "seq 1", func() bool { return e.Stats().Seq == 1 })
	if got := e.Runs().Runs[0].Status; got != herdr.StatusWorking {
		t.Fatalf("initial status = %q, want working", got)
	}

	src.set(runSnapshot(herdr.StatusBlocked), nil)
	e.Wake()
	waitFor(t, "seq 2", func() bool { return e.Stats().Seq == 2 })

	if got := e.Runs().Runs[0].Status; got != herdr.StatusBlocked {
		t.Fatalf("status = %q, want blocked", got)
	}
}

// A workspace changing checkout must rebroadcast: the run's repository context is
// part of the hashed topology content.
func TestWorktreeChangeChangesTopologyHash(t *testing.T) {
	t.Parallel()
	a := runSnapshot(herdr.StatusIdle)
	b := runSnapshot(herdr.StatusIdle)
	b.Workspaces[0].Worktree.CheckoutPath = "/code/space-api-other"
	if hashTopology(a) == hashTopology(b) {
		t.Fatal("a changed checkout path must change the topology hash")
	}
	c := runSnapshot(herdr.StatusIdle)
	c.Workspaces[0].Worktree = nil
	if hashTopology(a) == hashTopology(c) {
		t.Fatal("dropping the worktree must change the topology hash")
	}
}
