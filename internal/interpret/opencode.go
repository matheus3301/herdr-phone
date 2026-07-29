package interpret

import (
	"strings"
)

// OpenCode grammar, observed against 1.18.4 (SPEC §12.2).
//
// OpenCode frames its output with a `┃` gutter and marks activity with leading
// glyphs, but writes assistant prose as plain indented text with no marker. So
// unlike the Claude pass, an unmarked line here *is* content — which makes the
// frame-stripping in pass 1 load-bearing rather than merely tidy.
//
// Observed markers:
//
//	→  tool call            (`→ Read notes.txt`)
//	✱  search tool          (`✱ Glob "notes.txt" in . (1 match)`)
//	←  tool result          (`← Patched /path/to/file`)
//	~  in-progress status   (`~ Preparing patch...`)
//	▣  model/status footer  (`▣  Build · GPT-5.6 · 18.7s`)
//	△  permission required  (the interaction header)
const (
	openCodeToolCall   = '→'
	openCodeSearch     = '✱'
	openCodeToolResult = '←'
	openCodeWorking    = '~'
	openCodeStatus     = '▣'
	openCodeWarning    = '△'
	// openCodeThought prefixes the reasoning/progress readout (`+ Thought: … · 4.1s`).
	// It cannot collide with a diff row: those require a leading line number.
	openCodeThought = '+'
)

// openCodePermissionHeading is the text beside △ when the agent is blocked. It is
// matched case-insensitively on a prefix so a trailing qualifier still matches.
const openCodePermissionHeading = "permission required"

// openCodeButtons are the choices OpenCode offers on its selection row.
//
// They are recognized by label because there is no ordinal to parse. Critically,
// none of them is ever given a SendKey: which button is highlighted is carried by
// SGR styling, and the relay reads `format: text`, which discards it. Answering
// would mean guessing how many Tab presses separate the invisible current
// selection from the target — and guessing wrong selects Reject when the operator
// meant Allow once. See SPEC §12.2 and §21.
var openCodeButtons = []string{"Allow once", "Allow always", "Allow", "Reject", "Deny"}

// parseOpenCode interprets OpenCode pane text.
func parseOpenCode(lines []string, lim Limits) Result {
	var res Result

	clean := stripFrame(lines, &res.DroppedLines)

	body := clean
	if at, ok := openCodePermissionAt(texts(clean)); ok {
		res.Interaction = openCodeInteraction(texts(clean), at, lim)
		body = clean[:at]
	}

	res.Turns = openCodeTurns(body, lim, &res.DroppedLines)
	return res
}

// openCodePermissionAt finds the last `△ Permission required` heading. The last
// one wins for the same reason as Claude's option block: scrollback can hold
// prompts that were already answered.
func openCodePermissionAt(lines []string) (int, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		text, ok := trimMarker(lines[i], openCodeWarning)
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.ToLower(text), openCodePermissionHeading) {
			return i, true
		}
	}
	return 0, false
}

// openCodeInteraction reconstructs the permission prompt.
//
// Layout observed at 1.18.4, after the `┃` gutter is stripped:
//
//	△ Permission required
//	  → Edit /path/to/notes.txt        <- the action
//	                                   <- blank
//	1   sample file for the fixture    <- diff, context
//	2 + hello fixture                  <- diff, added
//	                                   <- blank
//	 Allow once   Allow always   Reject  ctrl+f fullscreen  ⇆ select  enter confirm
func openCodeInteraction(lines []string, at int, lim Limits) *Interaction {
	in := &Interaction{Kind: InteractionApproval}
	if text, ok := trimMarker(lines[at], openCodeWarning); ok {
		in.Question = strings.TrimSpace(text)
	}

	for i := at + 1; i < len(lines); i++ {
		line := lines[i]
		if isBlank(line) {
			continue
		}

		// The action line names the tool and its target; it becomes the title.
		if in.Title == "" {
			if text, ok := trimMarker(line, openCodeToolCall); ok {
				in.Title = strings.TrimSpace(text)
				continue
			}
		}

		if d, ok := parseOpenCodeDiffLine(line); ok {
			if len(in.Diff) < lim.MaxDiffLines {
				in.Diff = append(in.Diff, d)
			}
			continue
		}

		if opts, ok := parseOpenCodeButtons(line); ok {
			in.Options = append(in.Options, opts...)
			// The button row is the last thing in the prompt.
			break
		}

		if looksLikeHint(line) {
			continue
		}

		// Anything else under the heading is supporting detail.
		if len(in.Detail) < lim.MaxDetail {
			in.Detail = append(in.Detail, strings.TrimSpace(line))
		}
	}

	// Answerable is recomputed in bound(); every option here has an empty SendKey,
	// so it resolves to false. Stated explicitly for the reader.
	in.Answerable = false
	return in
}

// parseOpenCodeDiffLine reads `N   text`, `N + text`, or `N - text`.
//
// A line number followed by whitespace is required, which is what separates a diff
// row from prose that happens to begin with a digit.
func parseOpenCodeDiffLine(line string) (DiffLine, bool) {
	t := strings.TrimLeft(line, " \t")
	i := 0
	num := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		num = num*10 + int(t[i]-'0')
		i++
		if i > 7 { // absurd line number: not a diff row
			return DiffLine{}, false
		}
	}
	if i == 0 || i >= len(t) {
		return DiffLine{}, false
	}
	if t[i] != ' ' && t[i] != '\t' {
		return DiffLine{}, false
	}
	rest := t[i:]
	trimmed := strings.TrimLeft(rest, " \t")

	// `+`/`-` must be followed by a space to be an operation; `-foo` in prose is not.
	if strings.HasPrefix(trimmed, "+ ") {
		return DiffLine{Line: num, Op: DiffAdd, Text: strings.TrimSpace(trimmed[2:])}, true
	}
	if strings.HasPrefix(trimmed, "- ") {
		return DiffLine{Line: num, Op: DiffRemove, Text: strings.TrimSpace(trimmed[2:])}, true
	}
	// Context rows are separated from the number by two or more spaces. Requiring
	// that keeps `1 sentence about something` from parsing as a diff row.
	if len(rest) >= 2 && rest[0] == ' ' && rest[1] == ' ' {
		return DiffLine{Line: num, Op: DiffContext, Text: strings.TrimSpace(rest)}, true
	}
	return DiffLine{}, false
}

// parseOpenCodeButtons reads the horizontal selection row, returning options with
// no send key. Keyboard hints on the same row are excluded.
func parseOpenCodeButtons(line string) ([]Option, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil, false
	}
	// Require at least two recognized buttons so a sentence mentioning "Reject"
	// is not read as a prompt.
	found := 0
	for _, b := range openCodeButtons {
		if strings.Contains(trimmed, b) {
			found++
		}
	}
	if found < 2 {
		return nil, false
	}

	var opts []Option
	seen := make(map[string]bool, len(openCodeButtons))
	// Fields are separated by runs of two or more spaces on the rendered row.
	for _, field := range splitOnDoubleSpace(trimmed) {
		field = strings.TrimSpace(field)
		if field == "" || looksLikeHint(field) {
			continue
		}
		if !isOpenCodeButton(field) || seen[field] {
			continue
		}
		seen[field] = true
		// SendKey deliberately empty: see openCodeButtons.
		opts = append(opts, Option{Label: field})
	}
	if len(opts) < 2 {
		return nil, false
	}
	return opts, true
}

// isOpenCodeButton reports whether a field is exactly one of the known buttons.
// An exact match is required so an arbitrary field on the row cannot become a
// choice presented to the operator.
func isOpenCodeButton(field string) bool {
	for _, b := range openCodeButtons {
		if strings.EqualFold(field, b) {
			return true
		}
	}
	return false
}

// splitOnDoubleSpace splits a rendered row on runs of two or more spaces.
func splitOnDoubleSpace(s string) []string {
	var out []string
	start := 0
	i := 0
	for i < len(s) {
		if s[i] == ' ' && i+1 < len(s) && s[i+1] == ' ' {
			if start < i {
				out = append(out, s[start:i])
			}
			for i < len(s) && s[i] == ' ' {
				i++
			}
			start = i
			continue
		}
		i++
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// openCodeTurns builds the turn list. Unmarked lines are prose, so a run of them
// accumulates into one agent_text turn rather than one turn per screen row.
func openCodeTurns(lines []screenLine, lim Limits, dropped *int) []Turn {
	var turns []Turn
	// proseOpen tracks whether the last turn is an agent_text turn still being
	// accumulated, so wrapped prose rejoins into a paragraph.
	proseOpen := false

	for _, row := range lines {
		line := row.text
		if isBlank(line) {
			proseOpen = false
			continue
		}
		if looksLikeHint(line) {
			*dropped++
			proseOpen = false
			continue
		}

		// A gutter-framed row is the output of something OpenCode *ran* — a shell
		// block, a patch, a fetch — not the agent's own words. Without this it read
		// as prose, so `$ kubectl …` and `(no output)` were presented as things the
		// agent said. Framing is the only signal that distinguishes them, which is
		// why stripFrame preserves it.
		if row.framed {
			turns = append(turns, Turn{Kind: TurnToolResult, Text: strings.TrimSpace(line)})
			proseOpen = false
			continue
		}

		// `+ Thought: …` is OpenCode's reasoning/progress indicator. It is a status
		// readout, and rendering it as prose put "+ Thought: Considering UI errors"
		// in a bubble as though the agent had written it.
		if text, ok := trimMarker(line, openCodeThought); ok {
			turns = append(turns, Turn{Kind: TurnStatus, Text: text})
			proseOpen = false
			continue
		}

		if text, ok := trimMarker(line, openCodeToolCall); ok {
			tool, arg := splitOpenCodeTool(text)
			turns = append(turns, Turn{Kind: TurnToolCall, Tool: tool, Text: arg})
			proseOpen = false
			continue
		}
		if text, ok := trimMarker(line, openCodeSearch); ok {
			tool, arg := splitOpenCodeTool(text)
			turns = append(turns, Turn{Kind: TurnToolCall, Tool: tool, Text: arg})
			proseOpen = false
			continue
		}
		if text, ok := trimMarker(line, openCodeToolResult); ok {
			turns = append(turns, Turn{Kind: TurnToolResult, Text: text})
			proseOpen = false
			continue
		}
		if text, ok := trimMarker(line, openCodeWorking); ok {
			turns = append(turns, Turn{Kind: TurnStatus, Text: text})
			proseOpen = false
			continue
		}
		if text, ok := trimMarker(line, openCodeStatus); ok {
			turns = append(turns, Turn{Kind: TurnStatus, Text: text})
			proseOpen = false
			continue
		}
		// A diff row outside a permission prompt is a rendered patch: tool output,
		// not prose.
		if d, ok := parseOpenCodeDiffLine(line); ok {
			turns = append(turns, Turn{Kind: TurnToolResult, Text: d.Text})
			proseOpen = false
			continue
		}

		text := strings.TrimSpace(line)
		if proseOpen && len(turns) > 0 {
			last := &turns[len(turns)-1]
			if len(last.Text)+len(text)+1 <= lim.MaxTextLen {
				last.Text = strings.TrimSpace(last.Text + " " + text)
				continue
			}
			*dropped++
			continue
		}
		turns = append(turns, Turn{Kind: TurnAgentText, Text: text})
		proseOpen = true
	}
	return turns
}

// splitOpenCodeTool splits `Read notes.txt` into tool name and argument. OpenCode
// writes the tool as the first word; when it is not a plain identifier the whole
// text becomes the argument and the name is left empty rather than invented.
func splitOpenCodeTool(text string) (tool, arg string) {
	first := firstWord(text)
	if first == "" || !isToolName(first) {
		return "", strings.TrimSpace(text)
	}
	return first, strings.TrimSpace(strings.TrimPrefix(text, first))
}
