package interpret

import (
	"strings"
	"testing"
)

// Regression tests for shapes found by running this parser against real
// Herdr-rendered panes (`herdr pane read --source recent-unwrapped --format text`)
// rather than against hand-written fixtures.
//
// The content below is neutral by design. The real captures contained private
// working material and this repository is public, so the *shapes* are reproduced
// here and the captures themselves were never committed.
//
// Each test corresponds to a defect the real data exposed and synthetic fixtures
// did not, because a hand-written fixture naturally puts the whole exchange inside
// the window and uses only the markers the author already knew about.

// A bounded read of a busy Claude pane frequently contains no `⏺` at all: the
// answer began above the window. Treating those lines as unmarked chrome discarded
// the entire answer and left a transcript holding only the spinner line — strictly
// worse than falling back to the raw tail.
func TestClaudeRecoversAnswerWhoseMarkerScrolledOff(t *testing.T) {
	// Exactly the observed shape: indented continuation rows, then a status line,
	// then the footer chrome.
	screen := strings.Join([]string{
		"  the change touches three files and the counts line up",
		"  (spec 24, security 10, install 32).",
		"  - go test ./internal/app/: ok.",
		"",
		"✻ Crunched for 6m 11s",
		"                      new task? /clear to save 211k tokens",
		"────────────────────────────────────────────────────────────",
		"❯ yes, do that too",
	}, "\n")

	res, ok := Parse(KindClaude, screen, DefaultLimits())
	if !ok {
		t.Fatal("expected a match")
	}
	if !res.PartialLead {
		t.Error("a window opening mid-answer must be reported as a partial lead")
	}

	var prose []Turn
	for _, turn := range res.Turns {
		if turn.Kind == TurnAgentText {
			prose = append(prose, turn)
		}
	}
	if len(prose) != 1 {
		t.Fatalf("expected the answer recovered as one turn, got %#v", res.Turns)
	}
	for _, want := range []string{"touches three files", "spec 24", "go test ./internal/app/: ok."} {
		if !strings.Contains(prose[0].Text, want) {
			t.Errorf("recovered turn is missing %q: %q", want, prose[0].Text)
		}
	}
}

// The status line must not absorb the chrome row beneath it. It did, producing
// turns like "Crunched for 3m 48s new task? /clear to save 276k tokens".
func TestClaudeStatusDoesNotSwallowTheFooter(t *testing.T) {
	screen := strings.Join([]string{
		"⏺ done.",
		"",
		"✻ Crunched for 3m 48s",
		"                      new task? /clear to save 276.4k tokens",
	}, "\n")

	res, ok := Parse(KindClaude, screen, DefaultLimits())
	if !ok {
		t.Fatal("expected a match")
	}
	for _, turn := range res.Turns {
		if turn.Kind != TurnStatus {
			continue
		}
		if turn.Text != "Crunched for 3m 48s" {
			t.Errorf("status turn = %q, want just the status; it absorbed the footer", turn.Text)
		}
	}
}

// A gutter-framed row is the output of something OpenCode *ran*. Rendering it as
// prose attributed shell output to the agent — the exact misattribution this
// package exists to avoid.
func TestOpenCodeFramedOutputIsNotAgentProse(t *testing.T) {
	screen := strings.Join([]string{
		"  ┃",
		"  ┃  # Running in service-api",
		"  ┃",
		"  ┃  $ kubectl get all -n staging",
		"  ┃",
		"  ┃  No resources found in staging namespace.",
		"  ┃",
		"",
		"     + Thought: Evaluating options · 4.1s",
		"",
		"     ✱ Glob \".scripts/*\" in service-api",
		"     → Read service-api/.scripts [limit=100]",
		"",
		"     Could not start the project.",
		"",
		"     ▣  Build · GPT-5.6 · 15m 56s",
	}, "\n")

	res, ok := Parse(KindOpenCode, screen, DefaultLimits())
	if !ok {
		t.Fatal("expected a match")
	}

	byKind := map[TurnKind][]string{}
	for _, turn := range res.Turns {
		byKind[turn.Kind] = append(byKind[turn.Kind], turn.Text)
	}

	// Everything that came out of the framed block is tool output.
	for _, framed := range []string{"# Running in service-api", "$ kubectl get all -n staging", "No resources found in staging namespace."} {
		found := false
		for _, got := range byKind[TurnToolResult] {
			if got == framed {
				found = true
			}
		}
		if !found {
			t.Errorf("framed row %q was not classified as tool output; turns: %#v", framed, res.Turns)
		}
		// And specifically must not be prose.
		for _, got := range byKind[TurnAgentText] {
			if strings.Contains(got, framed) {
				t.Errorf("framed row %q was rendered as the agent's own prose", framed)
			}
		}
	}

	// The agent's actual sentence is still prose.
	foundProse := false
	for _, got := range byKind[TurnAgentText] {
		if strings.Contains(got, "Could not start the project.") {
			foundProse = true
		}
	}
	if !foundProse {
		t.Errorf("real prose was lost; turns: %#v", res.Turns)
	}
}

// `+ Thought: …` is OpenCode's reasoning readout, not something the agent said.
func TestOpenCodeThoughtIsStatusNotProse(t *testing.T) {
	screen := "     + Thought: Considering UI errors · 1.8s\n\n     Here is the answer.\n"
	res, ok := Parse(KindOpenCode, screen, DefaultLimits())
	if !ok {
		t.Fatal("expected a match")
	}
	for _, turn := range res.Turns {
		if strings.Contains(turn.Text, "Considering UI errors") && turn.Kind != TurnStatus {
			t.Errorf("thought readout classified as %s, want status: %#v", turn.Kind, turn)
		}
	}
}

// An idle Claude pane showing only the welcome banner has no activity to report, so
// it must not parse — the UI then shows the raw tail instead of an empty chat.
func TestClaudeIdleBannerDoesNotParse(t *testing.T) {
	screen := strings.Join([]string{
		"",
		" ▐▛███▜▌   Claude Code v2.1.220",
		"▝▜█████▛▘  Opus 5 (1M context) · API Usage Billing",
		"  ▘▘ ▝▝    ~/www/project",
		"",
		" ⚠ 1 MCP server needs authentication · run /mcp",
		"",
		"                                                     0 tokens",
		"──────────────────────────────────────────────────────────────",
		"❯ ",
		"──────────────────────────────────────────────────────────────",
		"  Opus 5 (1M context)",
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents",
	}, "\n")

	if res, ok := Parse(KindClaude, screen, DefaultLimits()); ok {
		t.Errorf("an idle banner must not parse as agent activity: %#v", res.Turns)
	}
}
