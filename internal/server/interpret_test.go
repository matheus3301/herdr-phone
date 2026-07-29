package server

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// Experimental heuristic interpretation on the run contract (SPEC §12.2).
//
// The property that matters most is the first one: with the flag off, the contract
// is byte-identical to the non-experimental one. Everything else is additive.

// interpretedWire decodes a run response as a generic typed-part list, which is
// what the contract actually is.
type interpretedWire struct {
	Capabilities struct {
		HeuristicInterpretation bool     `json:"heuristic_interpretation"`
		InterpretationParsers   []string `json:"interpretation_parsers"`
		PartTypes               []string `json:"part_types"`
		StructuredMessages      bool     `json:"structured_messages"`
		StructuredInteractions  bool     `json:"structured_interactions"`
	} `json:"capabilities"`
	Parts []map[string]any `json:"parts"`
}

func (w interpretedWire) part(typ string) (map[string]any, bool) {
	for _, p := range w.Parts {
		if s, _ := p["type"].(string); s == typ {
			return p, true
		}
	}
	return nil, false
}

// claudeApprovalScreen is the shape captured from Claude Code 2.1.220.
const claudeApprovalScreen = "⏺ I'll append the line.\n" +
	"\n" +
	"⏺ Bash(echo hi >> notes.txt)\n" +
	"  ⎿  done\n" +
	"\n" +
	"────────────────────────────────────────────────────────────────────────────────\n" +
	" Bash command\n" +
	"\n" +
	"   echo \"hello fixture\" >> notes.txt\n" +
	"   Append line to notes.txt and verify\n" +
	"\n" +
	" Do you want to proceed?\n" +
	" ❯ 1. Yes\n" +
	"   2. Yes, and always allow access to sandbox/ from this project\n" +
	"   3. No\n" +
	"\n" +
	" Esc to cancel · Tab to amend · ctrl+e to explain\n"

func enableInterpretation(c *Config) {
	c.Interpretation = Interpretation{
		Enabled:  true,
		Parsers:  []string{"claude", "opencode"},
		MaxTurns: 60,
	}
}

func TestInterpretationOffByDefaultLeavesContractUnchanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.state.setContent("pane-1", []byte(claudeApprovalScreen))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var got interpretedWire
	decodeBody(t, resp, &got)

	if got.Capabilities.HeuristicInterpretation {
		t.Error("heuristic_interpretation must be false without the opt-in")
	}
	if len(got.Capabilities.InterpretationParsers) != 0 {
		t.Errorf("interpretation_parsers must be absent, got %#v", got.Capabilities.InterpretationParsers)
	}
	if len(got.Capabilities.PartTypes) != 1 || got.Capabilities.PartTypes[0] != partObservedTerminalOutput {
		t.Errorf("part_types = %#v, want only %q", got.Capabilities.PartTypes, partObservedTerminalOutput)
	}
	if len(got.Parts) != 1 {
		t.Fatalf("parts = %d, want exactly the observed-output part", len(got.Parts))
	}
	if _, ok := got.part(partInterpretedTranscript); ok {
		t.Error("a transcript part must not be emitted while the flag is off")
	}
	if _, ok := got.part(partInterpretedInteraction); ok {
		t.Error("an interaction part must not be emitted while the flag is off")
	}
}

func TestInterpretationEnabledAdvertisesAndEmits(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(enableInterpretation))
	h.state.setContent("pane-1", []byte(claudeApprovalScreen))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var got interpretedWire
	decodeBody(t, resp, &got)

	if !got.Capabilities.HeuristicInterpretation {
		t.Fatal("heuristic_interpretation must be advertised when enabled")
	}
	// Interpretation is a guess. It must never masquerade as authoritative
	// structured data, because the UI's whole gating model depends on that.
	if got.Capabilities.StructuredMessages || got.Capabilities.StructuredInteractions {
		t.Error("heuristic interpretation must not set any structured_* capability")
	}
	want := []string{partObservedTerminalOutput, partInterpretedTranscript, partInterpretedInteraction}
	if len(got.Capabilities.PartTypes) != len(want) {
		t.Errorf("part_types = %#v, want %#v", got.Capabilities.PartTypes, want)
	}

	// Additive: the raw tail is still there, so the UI can always fall back.
	observed, ok := got.part(partObservedTerminalOutput)
	if !ok {
		t.Fatal("the observed-output part must still be emitted")
	}
	if text, _ := observed["text"].(string); !strings.Contains(text, "Do you want to proceed?") {
		t.Error("observed output must be unchanged and complete")
	}

	transcript, ok := got.part(partInterpretedTranscript)
	if !ok {
		t.Fatal("expected a transcript part")
	}
	if exp, _ := transcript["experimental"].(bool); !exp {
		t.Error("the transcript part must be flagged experimental on the wire")
	}
	if p, _ := transcript["parser"].(string); p != "claude" {
		t.Errorf("parser = %q, want claude", p)
	}

	interaction, ok := got.part(partInterpretedInteraction)
	if !ok {
		t.Fatal("expected an interaction part")
	}
	if answerable, _ := interaction["answerable"].(bool); !answerable {
		t.Error("a numbered Claude prompt must be answerable")
	}
	if kind, _ := interaction["interaction"].(string); kind != "approval" {
		t.Errorf("interaction = %q, want approval", kind)
	}
	opts, _ := interaction["options"].([]any)
	if len(opts) != 3 {
		t.Fatalf("options = %#v, want 3", opts)
	}
	first, _ := opts[0].(map[string]any)
	if key, _ := first["send_key"].(string); key != "1" {
		t.Errorf("first send_key = %q, want \"1\"", key)
	}
}

// A pane whose agent kind is not in the configured list is never parsed, even
// when its content would match another agent's grammar.
func TestInterpretationSkipsUnlistedAgentKind(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(func(c *Config) {
		c.Interpretation = Interpretation{Enabled: true, Parsers: []string{"opencode"}, MaxTurns: 60}
	}))
	h.state.setContent("pane-1", []byte(claudeApprovalScreen))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var got interpretedWire
	decodeBody(t, resp, &got)

	// The fake run's agent kind is "claude", which is not configured here.
	if _, ok := got.part(partInterpretedTranscript); ok {
		t.Error("a pane running an unlisted agent kind must not be parsed")
	}
	if _, ok := got.part(partInterpretedInteraction); ok {
		t.Error("a pane running an unlisted agent kind must not be parsed")
	}
	// The capability is still advertised: the feature is on, just not for this pane.
	if !got.Capabilities.HeuristicInterpretation {
		t.Error("the capability reflects configuration, not one pane's eligibility")
	}
}

// A no-match is a normal outcome: no transcript part, and the raw tail carries the
// content instead.
func TestInterpretationNoMatchOmitsParts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(enableInterpretation))
	h.state.setContent("pane-1", []byte("$ go test ./...\nok  \tpkg\t0.5s\n$ \n"))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var got interpretedWire
	decodeBody(t, resp, &got)

	if _, ok := got.part(partInterpretedTranscript); ok {
		t.Error("plain shell output must not produce a transcript")
	}
	if len(got.Parts) != 1 {
		t.Errorf("parts = %d, want only the observed-output part", len(got.Parts))
	}
}

// The OpenCode asymmetry, asserted at the wire boundary: the prompt is published
// but carries no answer key, so the phone cannot mis-answer it (SPEC §12.2, §21).
func TestInterpretationOpenCodePromptIsNotAnswerable(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(enableInterpretation))
	// Rebind the fake pane to OpenCode; interpretation keys on the run's agent kind.
	runs := h.state.Runs().Runs
	runs[0].AgentKind = "opencode"
	h.state.setRuns(runs)
	h.state.setContent("pane-1", []byte(
		"     I’ll append the requested line.\n"+
			"     → Read notes.txt\n"+
			"\n"+
			"  ┃  △ Permission required\n"+
			"  ┃    → Edit /tmp/sandbox/notes.txt\n"+
			"  ┃\n"+
			"  ┃  1   sample file for the fixture capture\n"+
			"  ┃  2 + hello fixture\n"+
			"  ┃\n"+
			"  ┃   Allow once   Allow always   Reject  ctrl+f fullscreen  ⇆ select  enter confirm\n",
	))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var got interpretedWire
	decodeBody(t, resp, &got)

	interaction, ok := got.part(partInterpretedInteraction)
	if !ok {
		t.Fatal("the OpenCode prompt must still be detected and surfaced")
	}
	if answerable, _ := interaction["answerable"].(bool); answerable {
		t.Error("an OpenCode selection row must never be answerable")
	}
	opts, _ := interaction["options"].([]any)
	if len(opts) < 2 {
		t.Fatalf("expected the button labels to be surfaced, got %#v", opts)
	}
	for _, o := range opts {
		m, _ := o.(map[string]any)
		if key, present := m["send_key"]; present && key != "" {
			t.Errorf("OpenCode option %v carries a send key %q", m["label"], key)
		}
	}
	// The diff being approved must survive, since that is what the operator reads.
	diff, _ := interaction["diff"].([]any)
	if len(diff) < 2 {
		t.Errorf("expected the diff rows, got %#v", diff)
	}
}

// Interpreted content is display text derived from untrusted pane bytes. Nothing
// may carry a control character, and the audit trail must stay content-free.
func TestInterpretedPartsAreSanitized(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(enableInterpretation))
	h.state.setContent("pane-1", []byte(
		"⏺ hello\x1b[31m\x07 there\n"+
			" Do you want to proceed?\n"+
			" 1. Ye\x00s\n"+
			" 2. No\r\n",
	))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(raw)
	for _, bad := range []string{"\x1b", "\x07", "\x00", "\\u001b", "\\u0007", "\\u0000"} {
		if strings.Contains(body, bad) {
			t.Errorf("control sequence %q survived into the response", bad)
		}
	}

	var got interpretedWire
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got.part(partInterpretedInteraction); !ok {
		t.Fatal("expected the interaction to parse")
	}

	// The audit trail records counts and outcomes only — never interpreted text.
	for _, entry := range h.audit.entriesFor("run.read") {
		for _, bad := range []string{"hello", "proceed"} {
			if strings.Contains(entry.Result, bad) || strings.Contains(entry.Resource, bad) {
				t.Errorf("interpreted content leaked into the audit trail: %+v", entry)
			}
		}
	}
}

func TestInterpretationRespectsMaxTurns(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(func(c *Config) {
		c.Interpretation = Interpretation{Enabled: true, Parsers: []string{"claude"}, MaxTurns: 3}
	}))
	var b strings.Builder
	for range 40 {
		b.WriteString("⏺ a turn\n\n")
	}
	h.state.setContent("pane-1", []byte(b.String()))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var got interpretedWire
	decodeBody(t, resp, &got)

	transcript, ok := got.part(partInterpretedTranscript)
	if !ok {
		t.Fatal("expected a transcript part")
	}
	turns, _ := transcript["turns"].([]any)
	if len(turns) != 3 {
		t.Errorf("turns = %d, want the configured bound of 3", len(turns))
	}
	if dropped, _ := transcript["dropped_turns"].(float64); dropped <= 0 {
		t.Error("dropped turns must be reported so a short transcript is not read as complete")
	}
}
