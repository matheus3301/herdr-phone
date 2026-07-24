package herdr

import "testing"

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
