package interpret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matheus3301/herdr-phone/internal/security"
)

// The fixtures under testdata/ were reconstructed from real PTY captures of
// Claude Code 2.1.220 and OpenCode 1.18.4 rendered to an 80x50 screen grid, which
// is the shape Herdr's `pane read --format text` returns (SPEC §12.2). They are
// checked in so the suite stays deterministic and needs no agent, no API key, and
// no Herdr session.

func load(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	// Fixtures go through the same sanitization the relay applies before parsing,
	// so a test can never pass on input the production path would never produce.
	return security.SanitizeTextBlock(string(b))
}

func TestParseClaudeApproval(t *testing.T) {
	res, ok := Parse(KindClaude, load(t, "claude-approval.txt"), DefaultLimits())
	if !ok {
		t.Fatal("expected a match for the Claude approval fixture")
	}
	if res.Parser != KindClaude {
		t.Fatalf("parser = %q, want %q", res.Parser, KindClaude)
	}

	in := res.Interaction
	if in == nil {
		t.Fatal("expected an interaction")
	}
	if in.Kind != InteractionApproval {
		t.Errorf("kind = %q, want %q", in.Kind, InteractionApproval)
	}
	if in.Title != "Bash command" {
		t.Errorf("title = %q, want %q", in.Title, "Bash command")
	}
	if in.Question != "Do you want to proceed?" {
		t.Errorf("question = %q, want %q", in.Question, "Do you want to proceed?")
	}
	if !in.Answerable {
		t.Error("a numbered Claude prompt must be answerable")
	}

	wantDetail := []string{
		`echo "hello fixture" >> notes.txt && cat -A notes.txt | tail -3`,
		"Append line to notes.txt and verify",
	}
	if len(in.Detail) != len(wantDetail) {
		t.Fatalf("detail = %#v, want %#v", in.Detail, wantDetail)
	}
	for i, want := range wantDetail {
		if in.Detail[i] != want {
			t.Errorf("detail[%d] = %q, want %q", i, in.Detail[i], want)
		}
	}

	wantOpts := []Option{
		{Label: "Yes", SendKey: "1"},
		{Label: "Yes, and always allow access to sandbox/ from this project", SendKey: "2"},
		{Label: "No", SendKey: "3"},
	}
	if len(in.Options) != len(wantOpts) {
		t.Fatalf("options = %#v, want %#v", in.Options, wantOpts)
	}
	for i, want := range wantOpts {
		if in.Options[i] != want {
			t.Errorf("option[%d] = %#v, want %#v", i, in.Options[i], want)
		}
	}

	// The keyboard-hint footer is an instruction to whoever is at the keyboard, so
	// it must never be offered as a choice.
	for _, o := range in.Options {
		if looksLikeHint(o.Label) {
			t.Errorf("hint text leaked into options: %q", o.Label)
		}
	}

	// The prompt itself must not also be rendered as prose.
	for _, turn := range res.Turns {
		if strings.Contains(turn.Text, "Do you want to proceed") {
			t.Errorf("interaction text leaked into a turn: %#v", turn)
		}
	}
}

func TestParseClaudeTurns(t *testing.T) {
	res, ok := Parse(KindClaude, load(t, "claude-working.txt"), DefaultLimits())
	if !ok {
		t.Fatal("expected a match for the Claude working fixture")
	}
	if res.Interaction != nil {
		t.Errorf("no prompt is pending, got interaction %#v", res.Interaction)
	}

	var calls []string
	for _, turn := range res.Turns {
		if turn.Kind == TurnToolCall {
			calls = append(calls, turn.Tool)
		}
	}
	want := []string{"Read", "Edit"}
	if len(calls) != len(want) {
		t.Fatalf("tool calls = %#v, want %#v (turns: %#v)", calls, want, res.Turns)
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("call[%d] = %q, want %q", i, calls[i], w)
		}
	}

	kinds := map[TurnKind]int{}
	for _, turn := range res.Turns {
		kinds[turn.Kind]++
	}
	if kinds[TurnAgentText] < 2 {
		t.Errorf("expected the two prose turns, got %#v", res.Turns)
	}
	if kinds[TurnToolResult] < 2 {
		t.Errorf("expected the two tool results, got %#v", res.Turns)
	}
	if kinds[TurnStatus] < 1 {
		t.Errorf("expected the spinner status turn, got %#v", res.Turns)
	}

	// Chrome must not survive as content.
	for _, turn := range res.Turns {
		for _, bad := range []string{"auto mode on", "ctx:", "1836 tokens", "────"} {
			if strings.Contains(turn.Text, bad) {
				t.Errorf("chrome %q leaked into turn %#v", bad, turn)
			}
		}
	}
	if res.DroppedLines == 0 {
		t.Error("expected dropped chrome lines to be counted")
	}
}

func TestParseClaudeWrappedProseRejoins(t *testing.T) {
	res, ok := Parse(KindClaude, load(t, "claude-working.txt"), DefaultLimits())
	if !ok {
		t.Fatal("expected a match")
	}
	// The second prose turn wraps across two screen rows; it must read as one
	// sentence rather than two bubbles split mid-clause.
	found := false
	for _, turn := range res.Turns {
		if turn.Kind == TurnAgentText && strings.Contains(turn.Text, "registered in rawConfig") {
			found = true
			if !strings.HasPrefix(turn.Text, "The strict decoder rejects") {
				t.Errorf("wrapped prose did not rejoin: %q", turn.Text)
			}
		}
	}
	if !found {
		t.Errorf("wrapped prose turn missing: %#v", res.Turns)
	}
}

func TestParseClaudeTrustDialog(t *testing.T) {
	res, ok := Parse(KindClaude, load(t, "claude-trust.txt"), DefaultLimits())
	if !ok {
		t.Fatal("expected a match for the trust dialog")
	}
	in := res.Interaction
	if in == nil {
		t.Fatal("expected an interaction")
	}
	// "Yes, I trust this folder" / "No, exit" is a permission decision.
	if in.Kind != InteractionApproval {
		t.Errorf("kind = %q, want %q", in.Kind, InteractionApproval)
	}
	if !strings.HasSuffix(in.Question, "?") {
		t.Errorf("question = %q, want a trailing question mark", in.Question)
	}
	if !in.Answerable || len(in.Options) != 2 {
		t.Fatalf("expected 2 answerable options, got %#v", in.Options)
	}
	if in.Options[0].SendKey != "1" || in.Options[1].SendKey != "2" {
		t.Errorf("send keys = %q/%q, want 1/2", in.Options[0].SendKey, in.Options[1].SendKey)
	}
}

func TestParseOpenCodePermissionIsDetectedButNotAnswerable(t *testing.T) {
	res, ok := Parse(KindOpenCode, load(t, "opencode-permission.txt"), DefaultLimits())
	if !ok {
		t.Fatal("expected a match for the OpenCode permission fixture")
	}
	in := res.Interaction
	if in == nil {
		t.Fatal("expected an interaction")
	}
	if in.Kind != InteractionApproval {
		t.Errorf("kind = %q, want %q", in.Kind, InteractionApproval)
	}

	// The whole point of the OpenCode path: the prompt is surfaced, but which
	// button is highlighted is carried by SGR styling that `format: text` drops, so
	// no option may ever carry a send key (SPEC §12.2, §21).
	if in.Answerable {
		t.Error("an OpenCode selection row must never be answerable")
	}
	if len(in.Options) < 2 {
		t.Fatalf("expected the button labels to be surfaced, got %#v", in.Options)
	}
	for _, o := range in.Options {
		if o.SendKey != "" {
			t.Errorf("OpenCode option %q carries send key %q; must be empty", o.Label, o.SendKey)
		}
	}

	if !strings.Contains(in.Title, "notes.txt") {
		t.Errorf("title = %q, want the edited path", in.Title)
	}

	// The diff the operator is being asked to approve must survive.
	var added []string
	for _, d := range in.Diff {
		if d.Op == DiffAdd {
			added = append(added, d.Text)
		}
	}
	if len(added) != 1 || added[0] != "hello fixture" {
		t.Errorf("added diff lines = %#v, want [\"hello fixture\"]", added)
	}
	if len(in.Diff) < 2 {
		t.Errorf("expected the context row too, got %#v", in.Diff)
	}

	// Keyboard hints share the button row and must not become choices.
	for _, o := range in.Options {
		for _, bad := range []string{"ctrl+f", "select", "confirm"} {
			if strings.Contains(strings.ToLower(o.Label), bad) {
				t.Errorf("hint %q leaked into option %q", bad, o.Label)
			}
		}
	}
}

func TestParseOpenCodeTurns(t *testing.T) {
	res, ok := Parse(KindOpenCode, load(t, "opencode-permission.txt"), DefaultLimits())
	if !ok {
		t.Fatal("expected a match")
	}
	var tools []string
	for _, turn := range res.Turns {
		if turn.Kind == TurnToolCall {
			tools = append(tools, turn.Tool)
		}
	}
	if len(tools) != 2 || tools[0] != "Glob" || tools[1] != "Read" {
		t.Errorf("tool calls = %#v, want [Glob Read] (turns %#v)", tools, res.Turns)
	}
	// The status bar with cost and cwd is chrome.
	for _, turn := range res.Turns {
		for _, bad := range []string{"$0.36", "ctrl+p", "▀"} {
			if strings.Contains(turn.Text, bad) {
				t.Errorf("chrome %q leaked into turn %#v", bad, turn)
			}
		}
	}
}

func TestParseRejectsUnknownKindAndEmptyInput(t *testing.T) {
	if _, ok := Parse(Kind("cursor"), "⏺ hello", DefaultLimits()); ok {
		t.Error("an unknown agent kind must not parse")
	}
	if _, ok := Parse(KindClaude, "", DefaultLimits()); ok {
		t.Error("empty input must not parse")
	}
	if _, ok := Parse(KindClaude, "   \n \n", DefaultLimits()); ok {
		t.Error("blank input must not parse")
	}
}

// A no-match is a first-class outcome: the relay omits the transcript part and the
// UI shows the raw tail. Plain shell output must never be dressed up as a chat.
func TestParseNoMatchOnPlainShellOutput(t *testing.T) {
	plain := strings.Join([]string{
		"$ go test ./...",
		"ok  \tgithub.com/matheus3301/herdr-phone/internal/state\t0.512s",
		"ok  \tgithub.com/matheus3301/herdr-phone/internal/server\t1.204s",
		"$ ",
	}, "\n")
	if res, ok := Parse(KindClaude, plain, DefaultLimits()); ok {
		t.Errorf("plain shell output parsed as Claude activity: %#v", res)
	}
}

// Prose that merely contains numbers must not be read as an option list.
func TestNumberedBlockIgnoresProse(t *testing.T) {
	for _, line := range []string{
		"it took 1.5 seconds to finish",
		"1 sentence about something",
		"see section 2. for details",
	} {
		if _, _, ok := parseNumberedOption(line); ok {
			t.Errorf("parsed %q as an option", line)
		}
	}
	// A single numbered line is not a decision.
	if _, _, _, ok := findNumberedBlock([]string{"1. only one"}); ok {
		t.Error("a single numbered line must not form an option block")
	}
}

// The last option block wins, so an answered prompt further up the scrollback does
// not shadow the live one.
func TestLastNumberedBlockWins(t *testing.T) {
	lines := []string{
		" First question?",
		" 1. Alpha",
		" 2. Beta",
		"",
		" Second question?",
		" 1. Gamma",
		" 2. Delta",
	}
	_, _, run, ok := findNumberedBlock(lines)
	if !ok {
		t.Fatal("expected a block")
	}
	if run[0].label != "Gamma" {
		t.Errorf("first option = %q, want Gamma (the live prompt)", run[0].label)
	}
}

func TestSendKeyIsSynthesizedNotCopied(t *testing.T) {
	// An ordinal outside 1-9 cannot produce a key, which makes the whole
	// interaction unanswerable rather than partly answerable.
	opts := toOptions([]numberedOption{
		{ordinal: 1, label: "Yes"},
		{ordinal: 10, label: "Tenth"},
	})
	if opts[0].SendKey != "1" {
		t.Errorf("ordinal 1 -> %q, want \"1\"", opts[0].SendKey)
	}
	if opts[1].SendKey != "" {
		t.Errorf("ordinal 10 -> %q, want empty", opts[1].SendKey)
	}

	res := bound(Result{Interaction: &Interaction{
		Title:   "t",
		Options: opts,
	}}, DefaultLimits())
	if res.Interaction.Answerable {
		t.Error("an option without a key must make the interaction unanswerable")
	}
}

// A label that looks like a keystroke must still not become one: the key comes
// from the ordinal, never from screen text.
func TestSendKeyIgnoresLabelContent(t *testing.T) {
	text := strings.Join([]string{
		" Do you want to proceed?",
		" 1. 9",
		" 2. rm -rf /",
	}, "\n")
	res, ok := Parse(KindClaude, text, DefaultLimits())
	if !ok {
		t.Fatal("expected a match")
	}
	if got := res.Interaction.Options[0].SendKey; got != "1" {
		t.Errorf("send key = %q, want \"1\" from the ordinal, not the label", got)
	}
	if got := res.Interaction.Options[1].SendKey; got != "2" {
		t.Errorf("send key = %q, want \"2\"", got)
	}
}

func TestBoundsAreEnforced(t *testing.T) {
	var b strings.Builder
	for range 500 {
		b.WriteString("⏺ turn text here\n\n")
	}
	lim := DefaultLimits()
	lim.MaxTurns = 10
	res, ok := Parse(KindClaude, b.String(), lim)
	if !ok {
		t.Fatal("expected a match")
	}
	if len(res.Turns) != 10 {
		t.Errorf("turns = %d, want 10", len(res.Turns))
	}
	if res.DroppedTurns == 0 {
		t.Error("dropped turns must be reported")
	}
}

func TestEmittedTextIsSanitizedAndBounded(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxTextLen = 40
	long := strings.Repeat("é", 200) // multi-byte, to catch a mid-rune cut
	res, ok := Parse(KindClaude, "⏺ "+long, lim)
	if !ok {
		t.Fatal("expected a match")
	}
	for _, turn := range res.Turns {
		if len(turn.Text) > lim.MaxTextLen {
			t.Errorf("turn text is %d bytes, over the %d bound", len(turn.Text), lim.MaxTextLen)
		}
		if turn.Text != security.SanitizeLogLine(turn.Text) {
			t.Errorf("turn text is not sanitized: %q", turn.Text)
		}
		if strings.ContainsRune(turn.Text, '�') {
			t.Errorf("turn text was cut mid-rune: %q", turn.Text)
		}
	}
}

func TestSupportedAndParserKinds(t *testing.T) {
	if !Supported("claude") || !Supported("opencode") {
		t.Error("both implemented parsers must be reported as supported")
	}
	if Supported("cursor") || Supported("") {
		t.Error("an unimplemented kind must not be reported as supported")
	}
	if len(ParserKinds()) != 2 {
		t.Errorf("ParserKinds() = %#v, want exactly the two implemented parsers", ParserKinds())
	}
}
