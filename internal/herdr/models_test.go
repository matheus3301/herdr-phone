package herdr

import (
	"encoding/json"
	"testing"
)

// The run contract depends on three fields Herdr v0.7.5 (protocol 17) reports
// that the topology model must decode: WorkspaceInfo.worktree — the only worktree
// context in session.snapshot, which carries no top-level worktrees array — and
// AgentInfo.launch_pending and AgentInfo.title.
func TestSnapshotDecodesRunContractFields(t *testing.T) {
	t.Parallel()
	const payload = `{
	  "version":"0.7.5","protocol":17,
	  "workspaces":[{"workspace_id":"w1","number":1,"label":"space-api","focused":true,
	    "pane_count":1,"tab_count":1,"active_tab_id":"w1:t1","agent_status":"working",
	    "worktree":{"repo_key":"k1","repo_name":"space-api","repo_root":"/code/space-api",
	      "checkout_path":"/code/space-api-auth","is_linked_worktree":true}}],
	  "tabs":[],"panes":[],"layouts":[],
	  "agents":[{"terminal_id":"t1","agent":"claude","pane_id":"w1:p1","workspace_id":"w1",
	    "tab_id":"w1:t1","focused":true,"agent_status":"working","revision":7,
	    "launch_pending":true,"title":"Fix auth refresh","display_agent":"Claude Code",
	    "state_labels":{"phase":"editing"},"tokens":{"files":"3"}}]
	}`
	var snap Snapshot
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snap.Workspaces) != 1 || snap.Workspaces[0].Worktree == nil {
		t.Fatal("workspace worktree context not decoded")
	}
	wt := snap.Workspaces[0].Worktree
	if wt.RepoName != "space-api" || wt.RepoRoot != "/code/space-api" ||
		wt.CheckoutPath != "/code/space-api-auth" || !wt.IsLinkedWorktree || wt.RepoKey != "k1" {
		t.Errorf("worktree = %+v", *wt)
	}
	if len(snap.Agents) != 1 {
		t.Fatal("agent not decoded")
	}
	a := snap.Agents[0]
	if !a.LaunchPending {
		t.Error("launch_pending not decoded")
	}
	if a.Title != "Fix auth refresh" || a.DisplayAgent != "Claude Code" {
		t.Errorf("title/display_agent = %q / %q", a.Title, a.DisplayAgent)
	}

	// A workspace outside a git work tree reports no worktree, and unknown fields
	// (state_labels, tokens) are tolerated, not an error.
	var bare Snapshot
	if err := json.Unmarshal([]byte(`{"version":"0.7.5","protocol":17,
	  "workspaces":[{"workspace_id":"w2","tokens":{"a":"b"}}],"panes":[],"tabs":[],"agents":[],"layouts":[]}`), &bare); err != nil {
		t.Fatalf("decode bare: %v", err)
	}
	if bare.Workspaces[0].Worktree != nil {
		t.Error("a workspace with no worktree must decode as nil, not a zero value")
	}
}

// UpstreamCode is the relay's only channel for a structured failure distinction.
// The message must never be part of it: a Herdr message can quote pane content.
func TestUpstreamCodeExposesOnlyTheCode(t *testing.T) {
	t.Parallel()
	err := NewError("not_found", "pane w1:p1 not found while running `git push --force`")
	if got := err.UpstreamCode(); got != "not_found" {
		t.Fatalf("UpstreamCode = %q", got)
	}
	if !IsCode(err, "not_found") {
		t.Fatal("IsCode must still match")
	}
	// NewError sanitizes and bounds, like the internal constructor.
	esc := NewError(CodeInvalidParams, "bad\x1b]0;pwned\x07 params")
	if got := esc.Message; got != "bad]0;pwned params" {
		t.Fatalf("NewError message = %q, want control characters stripped", got)
	}
	if esc.UpstreamCode() != CodeInvalidParams {
		t.Fatalf("UpstreamCode = %q", esc.UpstreamCode())
	}
}

func TestOccupantFingerprintDetectsSwaps(t *testing.T) {
	t.Parallel()
	base := Pane{PaneID: "w1:p1", TerminalID: "term_a", Agent: "claude",
		AgentSession: &AgentSession{Source: "herdr:claude", Kind: "id", Value: "s1"}}

	same := base
	if base.OccupantFingerprint() != same.OccupantFingerprint() {
		t.Fatal("identical occupants must share a fingerprint")
	}

	// A new agent session in the same pane must change the fingerprint.
	swapped := base
	swapped.AgentSession = &AgentSession{Source: "herdr:claude", Kind: "id", Value: "s2"}
	if swapped.OccupantFingerprint() == base.OccupantFingerprint() {
		t.Fatal("session swap must change fingerprint")
	}

	// A different agent kind must change the fingerprint.
	kind := base
	kind.Agent = "codex"
	if kind.OccupantFingerprint() == base.OccupantFingerprint() {
		t.Fatal("agent kind change must change fingerprint")
	}

	// A fresh terminal (relaunched process) must change the fingerprint.
	term := base
	term.TerminalID = "term_b"
	if term.OccupantFingerprint() == base.OccupantFingerprint() {
		t.Fatal("terminal change must change fingerprint")
	}

	// An empty pane (no agent) still has a stable fingerprint.
	empty := Pane{PaneID: "w1:p2", TerminalID: "term_c"}
	if empty.OccupantFingerprint() == "" {
		t.Fatal("empty pane fingerprint must be non-empty and stable")
	}
}

func TestParseReadSource(t *testing.T) {
	t.Parallel()
	cases := map[string]ReadSource{
		"":                 SourceVisible,
		"visible":          SourceVisible,
		"recent":           SourceRecent,
		"recent_unwrapped": SourceRecentUnwrapped,
		"recent-unwrapped": SourceRecentUnwrapped, // HTTP hyphen form
		"detection":        SourceDetection,
	}
	for in, want := range cases {
		got, err := ParseReadSource(in)
		if err != nil || got != want {
			t.Errorf("ParseReadSource(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if _, err := ParseReadSource("bogus"); err == nil {
		t.Error("bogus source must error")
	}
}
