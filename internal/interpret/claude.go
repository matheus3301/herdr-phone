package interpret

import "strings"

// Claude Code grammar, observed against 2.1.220 (SPEC §12.2).
//
// The parser is **marker-driven**: Claude Code prefixes every semantic line with a
// glyph, so a line without a known marker (and not an indented continuation of a
// marked line) is treated as chrome and dropped. That is deliberately conservative
// — it means a redesigned frame degrades to "no match" and the raw tail, instead of
// to a transcript full of window furniture.
//
// Observed markers:
//
//	⏺  assistant text, and tool calls in the form `⏺ Bash(cmd)`
//	⎿  tool result
//	✻  status / spinner line (`✻ Worked for 4s · ↓ 139 tokens`)
//	❯  the input line
const (
	claudeBullet     = '⏺'
	claudeToolResult = '⎿'
	claudeInput      = '❯'
)

// claudeSpinners are the glyphs Claude Code cycles through on its status line. The
// set is wider than any one release uses because the animation frames change
// freely; treating an unknown one as prose would put a spinner in a chat bubble.
const claudeSpinners = "✻✽✢✳✶✷✸✹✺✱∗⋆·"

// parseClaude interprets Claude Code pane text.
func parseClaude(lines []string, lim Limits) Result {
	var res Result

	// Pass 1: drop the frame. Gutters are stripped so framed content survives, and
	// every dropped line is counted so the UI can say how much was removed.
	clean := stripFrame(lines, &res.DroppedLines)
	clean = dropBannerRows(clean, &res.DroppedLines)

	// Pass 1b: the input line ends the transcript. Claude Code puts the composer at
	// the bottom and its own footer (model name, mode banner, cwd) *below* that, so
	// anything after the last `❯` is chrome by construction. Without this boundary
	// the partial-turn recovery adopted footer rows like "Opus 5 (1M context)" as
	// the agent's prose.
	clean = truncateAtLastInput(clean, &res.DroppedLines)

	// Pass 2: the live interaction, if any. It is located first because the turn
	// pass must not also render the prompt as prose.
	body := clean
	if start, _, run, ok := findNumberedBlock(texts(clean)); ok {
		carrier := claudeInteraction(texts(clean), start, run)
		res.Interaction = carrier.Interaction
		// Everything from the prompt's heading onward belongs to the interaction, so
		// the turn pass must not also render it as prose.
		cut := start
		if carrier.headStart >= 0 && carrier.headStart < cut {
			cut = carrier.headStart
		}
		body = clean[:cut]
	}

	// Pass 3: turns.
	res.Turns, res.PartialLead = claudeTurns(body, lim, &res.DroppedLines)
	return res
}

// isInputRow reports whether a row is the composer prompt.
//
// `❯` is overloaded: it prefixes the input line *and* marks the highlighted row of a
// numbered option list (`❯ 1. Yes`). Treating the latter as the composer truncated
// the transcript right through the option block and made every approval prompt
// disappear, so an option row is explicitly excluded.
func isInputRow(text string) bool {
	if !hasMarker(text, claudeInput) {
		return false
	}
	_, _, isOption := parseNumberedOption(text)
	return !isOption
}

// dropBannerRows removes rows whose first visible character is a box/block glyph.
// Those are the logo rows of the welcome banner, which carry text on the same line
// and so survive isRuleLine.
func dropBannerRows(lines []screenLine, dropped *int) []screenLine {
	out := make([]screenLine, 0, len(lines))
	for _, l := range lines {
		if !l.framed && startsWithBoxGlyph(l.text) {
			*dropped++
			continue
		}
		out = append(out, l)
	}
	return out
}

// truncateAtLastInput cuts everything from the last input-prompt row onward.
//
// The last `❯` is the composer; the agent's own transcript is entirely above it, and
// Claude Code's persistent footer is entirely below it. Cutting here is what keeps
// footer chrome out of the transcript without having to enumerate its wording,
// which changes between releases.
func truncateAtLastInput(lines []screenLine, dropped *int) []screenLine {
	cut := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if isInputRow(lines[i].text) {
			cut = i
			break
		}
	}
	if cut < 0 {
		return lines
	}
	*dropped += len(lines) - cut
	return lines[:cut]
}

// claudeInteractionCarrier carries the interaction plus where its heading began,
// so the caller can exclude the whole prompt from the transcript.
type claudeInteractionCarrier struct {
	*Interaction
	headStart int
}

// claudeInteraction reconstructs the prompt around a numbered option block.
//
// Layout observed at 2.1.220:
//
//	Bash command                     <- title, low indent
//	                                 <- blank
//	  echo "hello fixture" >> ...    <- detail, deeper indent
//	  Append line to notes.txt       <- detail
//	                                 <- blank
//	Do you want to proceed?          <- question, same indent as title
//	❯ 1. Yes                         <- option block
func claudeInteraction(lines []string, start int, run []numberedOption) *claudeInteractionCarrier {
	opts := toOptions(run)
	in := &Interaction{
		Kind:    classifyInteraction(opts),
		Options: opts,
	}
	headStart := start

	// Walk back over blanks to the nearest visible line.
	i := start - 1
	for i >= 0 && isBlank(lines[i]) {
		i--
	}
	// A trailing '?' identifies the question. Without one the prompt still parses;
	// the title then carries the meaning.
	questionIndent := -1
	if i >= 0 && strings.HasSuffix(strings.TrimSpace(lines[i]), "?") && !looksLikeHint(lines[i]) {
		in.Question = strings.TrimSpace(lines[i])
		questionIndent = indentOf(lines[i])
		headStart = i
		i--
	}

	// Detail lines are indented deeper than the question/title. Bounded so a long
	// scrollback cannot make this walk unbounded.
	var detail []string
	const maxBack = 40
	steps := 0
	for i >= 0 && steps < maxBack {
		steps++
		line := lines[i]
		if isBlank(line) {
			i--
			continue
		}
		if looksLikeHint(line) {
			break
		}
		ind := indentOf(line)
		if questionIndent >= 0 && ind <= questionIndent {
			// Same or shallower indentation than the question: this is the title.
			in.Title = strings.TrimSpace(line)
			headStart = i
			break
		}
		if questionIndent < 0 {
			// No question line was found, so indentation cannot be compared against
			// it. Take the nearest visible line as the title and stop.
			in.Title = strings.TrimSpace(line)
			headStart = i
			break
		}
		detail = append(detail, strings.TrimSpace(line))
		headStart = i
		i--
	}
	// Collected newest-first; restore reading order.
	for l, r := 0, len(detail)-1; l < r; l, r = l+1, r-1 {
		detail[l], detail[r] = detail[r], detail[l]
	}
	in.Detail = detail

	return &claudeInteractionCarrier{Interaction: in, headStart: headStart}
}

// claudeTurns builds the chat-shaped turn list from marker-prefixed lines.
//
// partialLead reports that the window opened part-way through a turn whose `⏺`
// marker had already scrolled off the top. That is the common case on a busy pane,
// not an edge case: Claude Code writes long answers, and a 40-line read of one
// frequently contains no marker at all. Dropping those lines as "unmarked" threw
// away the entire answer and left a chat containing only a spinner line — strictly
// worse than showing the raw tail. They are recovered as one turn instead, and the
// flag lets the UI say the turn started earlier than what it can show.
func claudeTurns(lines []screenLine, lim Limits, dropped *int) (turns []Turn, partialLead bool) {
	// openIndent is the indentation of the marker line whose text is still being
	// accumulated; a deeper-indented unmarked line continues it.
	openIndent := -1
	// sawMarker gates the leading-continuation recovery: once a real marker has
	// appeared, an unmarked line is either a continuation of it or chrome.
	sawMarker := false

	appendText := func(kind TurnKind, tool, text string, indent int) {
		turns = append(turns, Turn{Kind: kind, Tool: tool, Text: text})
		// A status line is a single-line readout, never a paragraph. Leaving it open
		// let the next indented chrome row continue it, which produced turns like
		// "Crunched for 3m 48s new task? /clear to save 276k tokens".
		if kind == TurnStatus {
			openIndent = -1
			return
		}
		openIndent = indent
	}

	for _, row := range lines {
		line := row.text
		if isBlank(line) {
			// A blank line closes the open turn but is not itself content.
			openIndent = -1
			continue
		}
		indent := indentOf(line)

		if text, ok := trimMarker(line, claudeBullet); ok {
			sawMarker = true
			if tool, arg, isCall := claudeToolCall(text); isCall {
				appendText(TurnToolCall, tool, arg, indent)
			} else {
				appendText(TurnAgentText, "", text, indent)
			}
			continue
		}
		if text, ok := trimMarker(line, claudeToolResult); ok {
			sawMarker = true
			appendText(TurnToolResult, "", text, indent)
			continue
		}
		if text, ok := claudeStatus(line); ok {
			sawMarker = true
			appendText(TurnStatus, "", text, indent)
			continue
		}
		if isInputRow(line) {
			// The input line. The operator's own instructions are recorded
			// authoritatively by the client when it sends them, so a screen echo is
			// never re-published as a turn — it would duplicate a real record with a
			// guessed one.
			*dropped++
			openIndent = -1
			continue
		}

		// An unmarked line continues the open turn when it is indented at least as
		// deep as the marker line that opened it.
		if openIndent >= 0 && indent >= openIndent && len(turns) > 0 {
			last := &turns[len(turns)-1]
			if len(last.Text)+len(line)+1 <= lim.MaxTextLen {
				last.Text = strings.TrimSpace(last.Text + " " + strings.TrimSpace(line))
				continue
			}
			// The turn is already at its bound; further continuation is dropped
			// rather than silently replacing earlier text.
			*dropped++
			continue
		}

		// Before any marker has been seen, an indented line is the tail of a turn
		// that began above the window. Recover it as one turn rather than discarding
		// the agent's whole answer. Requiring indentation is what keeps this from
		// swallowing the welcome banner and other flush-left chrome.
		if !sawMarker && indent >= claudeContinuationIndent {
			partialLead = true
			if len(turns) == 0 {
				turns = append(turns, Turn{Kind: TurnAgentText, Text: strings.TrimSpace(line)})
				openIndent = indent
				continue
			}
			last := &turns[len(turns)-1]
			if len(last.Text)+len(line)+1 <= lim.MaxTextLen {
				last.Text = strings.TrimSpace(last.Text + " " + strings.TrimSpace(line))
				continue
			}
			*dropped++
			continue
		}

		// Unmarked and unattached: frame.
		*dropped++
	}
	return turns, partialLead
}

// claudeContinuationIndent is the indentation Claude Code uses for the wrapped
// body of a turn. Its chrome (banners, hints, the input line) sits at 0 or 1.
const claudeContinuationIndent = 2

// claudeToolCall recognizes `Bash(cmd)` / `Read(path)` shaped text, returning the
// tool name and its argument.
//
// The name must be a capitalized identifier and the line must end in the closing
// parenthesis, so prose that merely contains parentheses is not mistaken for a
// call.
func claudeToolCall(text string) (tool, arg string, ok bool) {
	open := strings.IndexByte(text, '(')
	if open <= 0 || !strings.HasSuffix(text, ")") {
		return "", "", false
	}
	name := text[:open]
	if !isToolName(name) {
		return "", "", false
	}
	arg = text[open+1 : len(text)-1]
	return name, strings.TrimSpace(arg), true
}

// isToolName reports whether s looks like a tool identifier: starts uppercase,
// then letters, digits, underscores, or hyphens, and short enough to be a name.
func isToolName(s string) bool {
	if s == "" || len(s) > maxToolNameLen {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r < 'A' || r > 'Z' {
				return false
			}
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// claudeStatus recognizes a spinner/status line by its leading animation glyph.
func claudeStatus(line string) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	r := []rune(t)
	if len(r) == 0 || !strings.ContainsRune(claudeSpinners, r[0]) {
		return "", false
	}
	text := strings.TrimSpace(string(r[1:]))
	if text == "" {
		return "", false
	}
	return text, true
}
