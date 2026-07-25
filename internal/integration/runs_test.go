package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/herdr"
	"github.com/matheus3301/herdr-phone/internal/server"
	"github.com/matheus3301/herdr-phone/internal/state"
)

// runTopology is a session.snapshot payload in the exact shape Herdr v0.7.5
// (protocol 17) emits, including the fields the run contract depends on:
// WorkspaceInfo.worktree, AgentInfo.launch_pending, and AgentInfo.title. The
// topology carries no worktrees array, because session.snapshot has none — the
// only worktree context is the per-workspace object.
const runTopology = `{
  "version": "0.7.5",
  "protocol": 17,
  "focused_workspace_id": "w1",
  "focused_tab_id": "w1:t1",
  "focused_pane_id": "w1:p1",
  "workspaces": [{
    "workspace_id": "w1", "number": 1, "label": "space-api", "focused": true,
    "pane_count": 2, "tab_count": 1, "active_tab_id": "w1:t1", "agent_status": "blocked",
    "worktree": {
      "repo_key": "k1", "repo_name": "space-api", "repo_root": "/code/space-api",
      "checkout_path": "/code/space-api-auth", "is_linked_worktree": true
    }
  }],
  "tabs": [{
    "tab_id": "w1:t1", "workspace_id": "w1", "number": 1, "label": "agents",
    "focused": true, "pane_count": 2, "agent_status": "blocked"
  }],
  "panes": [
    {
      "pane_id": "w1:p1", "terminal_id": "term-1", "workspace_id": "w1", "tab_id": "w1:t1",
      "focused": true, "cwd": "/code/space-api-auth", "foreground_cwd": "/code/space-api-auth",
      "agent": "claude", "display_agent": "Claude Code", "title": "Fix auth refresh",
      "agent_status": "blocked", "revision": 42,
      "agent_session": {"source": "herdr:claude", "agent": "claude", "kind": "path", "value": "/Users/op/.claude/sessions/abc.json"}
    },
    {
      "pane_id": "w1:p2", "terminal_id": "term-2", "workspace_id": "w1", "tab_id": "w1:t1",
      "focused": false, "cwd": "/code/space-api", "agent_status": "unknown", "revision": 3
    }
  ],
  "agents": [{
    "terminal_id": "term-1", "agent": "claude", "name": "auth", "pane_id": "w1:p1",
    "workspace_id": "w1", "tab_id": "w1:t1", "agent_status": "blocked", "focused": true,
    "interactive_ready": true, "launch_pending": false, "state_change_seq": 9, "revision": 42,
    "cwd": "/code/space-api-auth", "title": "Fix auth refresh",
    "state_labels": {"phase": "awaiting approval"},
    "tokens": {"files": "3"}
  }],
  "layouts": []
}`

// runsAdapter starts a fake Herdr serving runTopology, drives one poll, and
// returns the state adapter under test.
func runsAdapter(t *testing.T) (*stateAdapter, *fakeHerdr, *state.Engine) {
	t.Helper()
	f := startFakeHerdr(t)
	f.setSnapshot(runTopology)
	f.setReadText("prompt> waiting for approval\n")

	client := herdr.NewClient(herdr.NewUnixDialer(f.path))
	engine, err := state.New(state.Config{Source: client})
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = engine.Run(ctx) }()

	deadline := time.After(3 * time.Second)
	for engine.Current() == nil {
		select {
		case <-deadline:
			t.Fatal("engine never produced a snapshot")
		case <-time.After(5 * time.Millisecond):
		}
	}
	ad := newStateAdapter(engine, client,
		capabilitiesBase{HerdrVersion: "0.7.5", HerdrProtocol: 17},
		newAgentKinds(&fakeKindsSource{kinds: []string{"claude"}}, time.Minute, time.Now), time.Now)
	return ad, f, engine
}

// The adapter must map every projected field onto the wire type with no
// synthesis: what the server serves is exactly what Herdr reported.
func TestStateAdapterRunsMapping(t *testing.T) {
	t.Parallel()
	ad, _, engine := runsAdapter(t)

	set := ad.Runs()
	if set.SnapshotHash == "" {
		t.Error("projection must carry the snapshot hash it came from")
	}
	runs := set.Runs
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (the shell pane is not a run)", len(runs))
	}
	r := runs[0]

	gen, ok := engine.Generation("w1:p1")
	if !ok {
		t.Fatal("pane has no generation")
	}
	if r.PaneGeneration != gen {
		t.Errorf("run generation = %d, engine generation = %d; the guard and the run must agree",
			r.PaneGeneration, gen)
	}
	if r.AgentIncarnation == "" {
		t.Error("missing agent incarnation")
	}
	// The handle is opaque but stable: pane, generation, and occupant digest, so a
	// recycled pane id restarting at generation 1 cannot reuse a dead run's id.
	if want := "w1:p1@1#" + r.AgentIncarnation; r.PaneID != "w1:p1" || r.RunID != want {
		t.Errorf("identity = %s / %s, want %s / %s", r.PaneID, r.RunID, "w1:p1", want)
	}
	if r.WorkspaceID != "w1" || r.WorkspaceLabel != "space-api" ||
		r.TabID != "w1:t1" || r.TabLabel != "agents" || r.TerminalID != "term-1" {
		t.Errorf("topology context = %+v", r)
	}
	if r.AgentKind != "claude" || r.AgentName != "auth" || r.DisplayAgent != "Claude Code" {
		t.Errorf("agent identity = %+v", r)
	}
	if r.Title != "Fix auth refresh" || r.Status != "blocked" {
		t.Errorf("title/status = %q / %q", r.Title, r.Status)
	}
	if !r.InteractiveReady || r.LaunchPending || !r.Focused {
		t.Errorf("flags = ready=%v pending=%v focused=%v", r.InteractiveReady, r.LaunchPending, r.Focused)
	}
	if r.CWD != "/code/space-api-auth" || r.ForegroundCWD != "/code/space-api-auth" {
		t.Errorf("cwd = %q / %q", r.CWD, r.ForegroundCWD)
	}
	if r.Revision != 42 || r.StateChangeSeq != 9 {
		t.Errorf("counters = rev=%d seq=%d", r.Revision, r.StateChangeSeq)
	}
	if r.Worktree == nil {
		t.Fatal("worktree context missing; session.snapshot's only source is WorkspaceInfo.worktree")
	}
	if r.Worktree.RepoName != "space-api" || r.Worktree.RepoRoot != "/code/space-api" ||
		r.Worktree.CheckoutPath != "/code/space-api-auth" || !r.Worktree.IsLinkedWorktree {
		t.Errorf("worktree = %+v", *r.Worktree)
	}
}

// The incarnation must not publish the agent session reference, which Herdr may
// report as a filesystem path.
func TestStateAdapterRunsHideSessionPath(t *testing.T) {
	t.Parallel()
	ad, _, _ := runsAdapter(t)
	const sessionPath = "/Users/op/.claude/sessions/abc.json"
	r := ad.Runs().Runs[0]
	if r.AgentIncarnation == sessionPath {
		t.Fatal("incarnation must be a digest, not the session path")
	}
	for _, field := range []string{r.RunID, r.AgentIncarnation, r.TerminalID, r.Title, r.DisplayAgent} {
		if field == sessionPath {
			t.Fatalf("session path leaked into %q", field)
		}
	}
}

// The adapter satisfies the full server contract, so a new StateProvider method
// cannot be forgotten at the composition root.
func TestStateAdapterImplementsRunProvider(t *testing.T) {
	t.Parallel()
	ad, _, _ := runsAdapter(t)
	var provider server.StateProvider = ad
	if got := provider.Runs(); len(got.Runs) != 1 {
		t.Fatalf("runs through the interface = %d, want 1", len(got.Runs))
	}
}

// Before the first successful poll there is nothing authoritative to report.
func TestStateAdapterRunsEmptyBeforePoll(t *testing.T) {
	t.Parallel()
	f := startFakeHerdr(t)
	client := herdr.NewClient(herdr.NewUnixDialer(f.path))
	engine, err := state.New(state.Config{Source: client})
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	ad := newStateAdapter(engine, client, capabilitiesBase{},
		newAgentKinds(&fakeKindsSource{}, time.Minute, time.Now), time.Now)
	if set := ad.Runs(); set.Runs != nil || set.SnapshotHash != "" {
		t.Fatalf("projection before the first poll = %+v, want zero", set)
	}
}

// A client fault inside the dispatcher must reach the relay as a structured
// invalid_params code, not as an unclassified internal fault: the browser is told
// to fix the request rather than to retry a broken one.
func TestDispatchClientFaultsCarryStructuredCode(t *testing.T) {
	t.Parallel()
	f := startFakeHerdr(t)
	client := herdr.NewClient(herdr.NewUnixDialer(f.path))
	kinds := newAgentKinds(&fakeKindsSource{kinds: []string{"claude"}}, time.Minute, time.Now)
	m := &mutatorAdapter{client: client, kinds: kinds}

	cases := map[string][2]string{
		"unknown field":       {"pane.focus", `{"pane_id":"w1:p1","nope":1}`},
		"wrong type":          {"pane.focus", `{"pane_id":42}`},
		"missing destination": {"pane.move", `{"pane_id":"w1:p1"}`},
		"unstartable kind":    {"agent.start", `{"pane_id":"w1:p1","kind":"nonesuch","name":"a"}`},
		"absent kind":         {"agent.start", `{"pane_id":"w1:p1","name":"a"}`},
	}
	for what, tc := range cases {
		_, err := m.Mutate(context.Background(), tc[0], []byte(tc[1]))
		if err == nil {
			t.Errorf("%s: want an error", what)
			continue
		}
		if !herdr.IsCode(err, herdr.CodeInvalidParams) {
			t.Errorf("%s: err = %v, want a structured %s", what, err, herdr.CodeInvalidParams)
		}
		var coder server.UpstreamCoder
		if !errors.As(err, &coder) || coder.UpstreamCode() != herdr.CodeInvalidParams {
			t.Errorf("%s: error does not expose an upstream code to the relay: %v", what, err)
		}
	}
}
