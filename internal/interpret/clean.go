package interpret

import (
	"strings"
	"unicode"
)

// Shared line-level cleanup. The input is a rendered screen grid, so most of what
// arrives is the TUI's own frame: borders, gutters, separator rules, status bars,
// and the input line. None of that is agent activity, and all of it has to go
// before the grammar-specific pass can see the content.
//
// Everything here is conservative in one direction on purpose: it is better to
// keep a chrome line (the operator sees a slightly noisy bubble) than to drop a
// content line (the operator silently loses something the agent said).

// gutterRunes are the vertical frame characters a TUI uses to the left of framed
// content. A line beginning with one is content *inside* a frame, so the prefix is
// stripped and the remainder kept.
const gutterRunes = "┃│┆┇┊┋║▌▐"

// stripGutter removes a leading frame gutter, returning the remaining text and
// whether a gutter was present.
//
// Only a *leading* gutter is removed. A trailing one is left alone because a
// two-column layout puts real content on both sides of it, and cutting at the
// first vertical bar would truncate the left column's text.
func stripGutter(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return "", false
	}
	r := []rune(trimmed)
	if !strings.ContainsRune(gutterRunes, r[0]) {
		return line, false
	}
	rest := string(r[1:])
	// Preserve relative indentation inside the frame: the grammar passes rely on
	// it to tell a title from its indented body.
	return strings.TrimRight(rest, " \t"), true
}

// boxRunes are the drawing characters that make up borders, rules, and the
// block-glyph logos these TUIs print. A line made only of these (plus spaces) is
// frame, never content.
func isBoxRune(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x257F: // Box Drawing
		return true
	case r >= 0x2580 && r <= 0x259F: // Block Elements
		return true
	case r == '─' || r == '━' || r == '═':
		return true
	}
	return false
}

// startsWithBoxGlyph reports whether a line's first visible character is a
// box-drawing or block glyph.
//
// Such a line is frame even when it also carries text, which `isRuleLine` cannot
// catch: Claude Code's banner draws its logo and its version string on the same row
// (`▐▛███▜▌   Claude Code v2.1.220`). Agent prose never opens with a block glyph, so
// this is a safe discriminator — and a necessary one, because the partial-turn
// recovery would otherwise adopt the banner as the agent's words.
func startsWithBoxGlyph(line string) bool {
	for _, r := range line {
		if r == ' ' || r == '\t' {
			continue
		}
		return isBoxRune(r)
	}
	return false
}

// isRuleLine reports whether a line is pure frame: only box-drawing/block glyphs
// and whitespace, with at least one such glyph.
func isRuleLine(line string) bool {
	seen := false
	for _, r := range line {
		switch {
		case r == ' ' || r == '\t':
		case isBoxRune(r):
			seen = true
		default:
			return false
		}
	}
	return seen
}

// isBlank reports whether a line carries no visible characters. Rendered grids are
// space-padded, so "blank" means whitespace-only, not empty.
func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

// indentOf counts leading spaces (tabs count as one). The grammar passes use
// relative indentation to attribute continuation lines to the turn above them.
func indentOf(line string) int {
	n := 0
	for _, r := range line {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}

// hintTokens are the keyboard-hint fragments a TUI appends to a prompt footer.
// They are instructions to whoever is at the keyboard, not options, and must never
// become selectable choices on the phone.
var hintTokens = []string{
	"esc to", "enter to", "tab to", "ctrl+", "shift+tab", "⇆", "↵",
	"to cancel", "to confirm", "to amend", "to explain", "to interrupt",
	"select", "fullscreen", "for more", "to cycle", "to expand", "to toggle",
}

// looksLikeHint reports whether a line is a keyboard-hint footer.
func looksLikeHint(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	if l == "" {
		return false
	}
	for _, t := range hintTokens {
		if strings.Contains(l, t) {
			return true
		}
	}
	return false
}

// trimMarker removes a leading marker rune and the whitespace after it, returning
// the remainder. It reports false when the line does not start with that marker,
// so callers can chain checks without re-trimming.
func trimMarker(line string, marker rune) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	r := []rune(t)
	if len(r) == 0 || r[0] != marker {
		return "", false
	}
	return strings.TrimSpace(string(r[1:])), true
}

// hasMarker reports whether the first visible rune is marker.
func hasMarker(line string, marker rune) bool {
	_, ok := trimMarker(line, marker)
	return ok
}

// screenLine is one row after frame-stripping.
//
// `framed` records that the row sat inside a gutter (`┃`) block. That signal has to
// survive the strip, because it is how OpenCode distinguishes *executed command
// output* from the agent's own prose — and attributing shell output to the agent is
// precisely the mistake this package must not make. Discarding it during cleanup
// caused exactly that: `$ kubectl …` and `(no output)` were rendered as agent_text.
type screenLine struct {
	text   string
	framed bool
}

// collapseBlanks removes leading/trailing blank lines and folds interior runs of
// blanks into one. A rendered grid is mostly padding, and without this every
// bubble would carry a screenful of empty rows.
func collapseBlanks(lines []screenLine) []screenLine {
	out := make([]screenLine, 0, len(lines))
	pendingBlank := false
	for _, l := range lines {
		if isBlank(l.text) {
			if len(out) > 0 {
				pendingBlank = true
			}
			continue
		}
		if pendingBlank {
			out = append(out, screenLine{})
			pendingBlank = false
		}
		out = append(out, l)
	}
	return out
}

// texts projects the plain text of each row, for the passes that only need it.
func texts(lines []screenLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.text
	}
	return out
}

// stripFrame is the shared first pass: drop rules and status bars, strip gutters,
// and collapse padding, counting everything removed.
func stripFrame(lines []string, dropped *int) []screenLine {
	out := make([]screenLine, 0, len(lines))
	for _, raw := range lines {
		text, framed := stripGutter(raw)
		if isRuleLine(text) || isStatusNoise(text) {
			*dropped++
			continue
		}
		out = append(out, screenLine{text: text, framed: framed})
	}
	return collapseBlanks(out)
}

// isStatusNoise reports whether a line is a persistent status/footer bar rather
// than a moment of agent activity: token counters, context percentages, cost
// readouts, model/mode banners, and cwd bars.
//
// These are matched by shape, not by a vocabulary list, because their wording
// changes between releases while their shape does not.
func isStatusNoise(line string) bool {
	l := strings.TrimSpace(line)
	if l == "" {
		return false
	}
	low := strings.ToLower(l)

	switch {
	// "1836 tokens", "49.2K (5%) · $0.36"
	case endsWithWord(low, "tokens") && startsWithNumber(low):
		return true
	// "Opus 5 (1M context) | ctx: 0%"
	case strings.Contains(low, "ctx:"):
		return true
	// "⏸ manual mode on", "⏵⏵ auto mode on (shift+tab to cycle)"
	case strings.Contains(low, "mode on"), strings.Contains(low, "mode off"):
		return true
	// A cost readout is always a currency amount next to a separator dot.
	case strings.Contains(l, "$") && strings.Contains(l, "·"):
		return true
	}
	return false
}

func startsWithNumber(s string) bool {
	for _, r := range s {
		return unicode.IsDigit(r)
	}
	return false
}

func endsWithWord(s, word string) bool {
	s = strings.TrimRight(s, " .")
	return strings.HasSuffix(s, word)
}
