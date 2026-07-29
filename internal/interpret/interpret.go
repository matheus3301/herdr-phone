package interpret

import (
	"strings"

	"github.com/matheus3301/herdr-phone/internal/security"
)

// Kind is an agent kind whose grammar this package knows. It matches Herdr's
// agent identifier, which is what the run projection reports.
type Kind string

const (
	KindClaude   Kind = "claude"
	KindOpenCode Kind = "opencode"
)

// ParserKinds is the complete set of recognized parsers, in a stable order. It is
// the allowlist `experimental.agent_output_parsers` is validated against, so a
// typo in config fails at start instead of silently parsing nothing.
func ParserKinds() []Kind { return []Kind{KindClaude, KindOpenCode} }

// Supported reports whether s names a parser this build implements.
func Supported(s string) bool {
	for _, k := range ParserKinds() {
		if string(k) == s {
			return true
		}
	}
	return false
}

// TurnKind classifies one interpreted turn. A consumer must ignore a kind it does
// not know rather than guess at it (SPEC §12.2).
type TurnKind string

const (
	// TurnAgentText is prose the agent appears to have written. It is still only
	// terminal text that looked like prose — never a quoted assistant message.
	TurnAgentText TurnKind = "agent_text"
	// TurnToolCall is an apparent tool invocation.
	TurnToolCall TurnKind = "tool_call"
	// TurnToolResult is output the TUI attributed to a tool.
	TurnToolResult TurnKind = "tool_result"
	// TurnStatus is a progress/status line (spinner text, timing, token counts).
	TurnStatus TurnKind = "status"
)

// Turn is one element of the chat-shaped reading.
type Turn struct {
	Kind TurnKind
	// Tool is the apparent tool name, set only for TurnToolCall. Empty otherwise.
	Tool string
	Text string
}

// DiffOp is one line's role in an interpreted diff.
type DiffOp string

const (
	DiffContext DiffOp = "context"
	DiffAdd     DiffOp = "add"
	DiffRemove  DiffOp = "remove"
)

// DiffLine is one line of an interpreted diff. Line is the number the TUI
// displayed; it is not verified against any file and may be zero when absent.
type DiffLine struct {
	Line int
	Op   DiffOp
	Text string
}

// Option is one choice an interaction appears to offer.
//
// Label is untrusted display text from the pane. SendKey is the literal key the
// phone would deliver to answer with this option, synthesized by this package
// from the parsed ordinal and validated against a single-digit allowlist. An
// empty SendKey means this option cannot be answered remotely and must not be
// offered as an action.
type Option struct {
	Label   string
	SendKey string
}

// InteractionKind distinguishes a permission decision from an open question.
type InteractionKind string

const (
	InteractionApproval InteractionKind = "approval"
	InteractionQuestion InteractionKind = "question"
)

// Interaction is the one prompt the pane appears to be blocked on.
type Interaction struct {
	Kind InteractionKind
	// Title is the prompt's heading — typically the tool or the action.
	Title string
	// Detail is the indented body beneath the title (a command, a description).
	Detail []string
	// Question is the interrogative line, when one was found.
	Question string
	// Answerable is true only when every offered option carries a valid SendKey.
	// It is false for a selection-row prompt whose highlight is invisible in text
	// mode, which is why it is reported separately from len(Options) > 0.
	Answerable bool
	Options    []Option
	// Diff is the change the prompt is asking about, when the TUI rendered one.
	Diff []DiffLine
}

// Result is one interpretation of one pane read.
type Result struct {
	Parser Kind
	Turns  []Turn
	// DroppedTurns counts turns discarded to fit Limits.MaxTurns; the oldest go
	// first, so the newest activity always survives.
	DroppedTurns int
	// DroppedLines counts input lines classified as TUI chrome and removed.
	DroppedLines int
	// PartialLead reports that the first turn began before the top of the window,
	// so it is a tail rather than a whole turn. A bounded read of a busy pane hits
	// this often, and the UI must say so rather than presenting a fragment as a
	// complete answer.
	PartialLead bool
	// Interaction is the live prompt, or nil when none was recognized.
	Interaction *Interaction
}

// Empty reports whether the result carries nothing worth publishing. The relay
// omits the transcript part entirely in that case rather than emitting an empty
// one, so the UI falls back to the raw tail.
func (r Result) Empty() bool { return len(r.Turns) == 0 && r.Interaction == nil }

// Limits bounds every dimension of the output so a hostile or merely enormous
// pane cannot turn one read into unbounded work or an unbounded response.
type Limits struct {
	MaxTurns     int
	MaxOptions   int
	MaxDetail    int
	MaxDiffLines int
	// MaxTextLen bounds each individual emitted string, in bytes.
	MaxTextLen int
	// MaxLines bounds how many input lines are examined at all.
	MaxLines int
}

// DefaultLimits are the bounds the relay uses. MaxTurns is overridden from
// `experimental.max_interpreted_turns`.
func DefaultLimits() Limits {
	return Limits{
		MaxTurns:     60,
		MaxOptions:   12,
		MaxDetail:    12,
		MaxDiffLines: 60,
		MaxTextLen:   2000,
		MaxLines:     2000,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxTurns <= 0 {
		l.MaxTurns = d.MaxTurns
	}
	if l.MaxOptions <= 0 {
		l.MaxOptions = d.MaxOptions
	}
	if l.MaxDetail <= 0 {
		l.MaxDetail = d.MaxDetail
	}
	if l.MaxDiffLines <= 0 {
		l.MaxDiffLines = d.MaxDiffLines
	}
	if l.MaxTextLen <= 0 {
		l.MaxTextLen = d.MaxTextLen
	}
	if l.MaxLines <= 0 {
		l.MaxLines = d.MaxLines
	}
	return l
}

// Parse interprets pane text for one agent kind.
//
// ok is false when the kind has no parser or when nothing was recognized. A false
// return is a normal, expected outcome and means "show the raw tail" — it is never
// an error to report to the operator.
//
// text must already be sanitized (see the package doc); Parse does not depend on
// that for safety, because everything it emits is sanitized again on the way out.
func Parse(kind Kind, text string, lim Limits) (Result, bool) {
	lim = lim.withDefaults()

	lines := splitLines(text, lim.MaxLines)
	if len(lines) == 0 {
		return Result{}, false
	}

	var res Result
	switch kind {
	case KindClaude:
		res = parseClaude(lines, lim)
	case KindOpenCode:
		res = parseOpenCode(lines, lim)
	default:
		return Result{}, false
	}
	res.Parser = kind

	res = bound(res, lim)
	if res.Empty() {
		return Result{}, false
	}
	return res, true
}

// splitLines splits into at most maxLines lines, keeping the *tail*. The most
// recent output is what an operator needs, matching boundObservedText's choice
// upstream.
func splitLines(text string, maxLines int) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

// bound applies Limits to a parser's raw output and sanitizes every emitted
// string. Turn trimming drops the oldest, so the newest activity survives.
func bound(res Result, lim Limits) Result {
	if len(res.Turns) > lim.MaxTurns {
		res.DroppedTurns += len(res.Turns) - lim.MaxTurns
		res.Turns = res.Turns[len(res.Turns)-lim.MaxTurns:]
	}
	out := make([]Turn, 0, len(res.Turns))
	for _, t := range res.Turns {
		t.Text = clamp(t.Text, lim.MaxTextLen)
		t.Tool = clamp(t.Tool, maxToolNameLen)
		// A turn whose text sanitized away carries nothing; drop it rather than
		// render an empty bubble. A tool call keeps its name as the content.
		if t.Text == "" && t.Tool == "" {
			continue
		}
		out = append(out, t)
	}
	res.Turns = out

	if res.Interaction != nil {
		in := *res.Interaction
		in.Title = clamp(in.Title, lim.MaxTextLen)
		in.Question = clamp(in.Question, lim.MaxTextLen)

		if len(in.Detail) > lim.MaxDetail {
			in.Detail = in.Detail[:lim.MaxDetail]
		}
		detail := make([]string, 0, len(in.Detail))
		for _, d := range in.Detail {
			if d = clamp(d, lim.MaxTextLen); d != "" {
				detail = append(detail, d)
			}
		}
		in.Detail = detail

		if len(in.Options) > lim.MaxOptions {
			in.Options = in.Options[:lim.MaxOptions]
		}
		opts := make([]Option, 0, len(in.Options))
		for _, o := range in.Options {
			o.Label = clamp(o.Label, maxOptionLabelLen)
			// Re-validate here as well as at construction. This is the last gate
			// before a key reaches a caller, and it is cheap.
			if !validSendKey(o.SendKey) {
				o.SendKey = ""
			}
			if o.Label == "" {
				continue
			}
			opts = append(opts, o)
		}
		in.Options = opts

		// Answerable is derived, never trusted from the parser: it holds only when
		// there is at least one option and every one of them can be answered.
		in.Answerable = len(in.Options) > 0
		for _, o := range in.Options {
			if o.SendKey == "" {
				in.Answerable = false
				break
			}
		}

		if len(in.Diff) > lim.MaxDiffLines {
			// Keep the tail: the end of a hunk is where the change usually is.
			in.Diff = in.Diff[len(in.Diff)-lim.MaxDiffLines:]
		}
		diff := make([]DiffLine, 0, len(in.Diff))
		for _, d := range in.Diff {
			d.Text = clamp(d.Text, lim.MaxTextLen)
			diff = append(diff, d)
		}
		in.Diff = diff

		// An interaction with no title, question, or options says nothing.
		if in.Title == "" && in.Question == "" && len(in.Options) == 0 {
			res.Interaction = nil
		} else {
			res.Interaction = &in
		}
	}
	return res
}

const (
	maxToolNameLen    = 64
	maxOptionLabelLen = 200
)

// clamp sanitizes one emitted string and bounds it to maxBytes without splitting
// a rune. SanitizeLogLine folds it to a single line, which is what a display
// field should be; multi-line content is carried as separate Detail/Diff entries.
func clamp(s string, maxBytes int) string {
	if s == "" {
		return ""
	}
	s = strings.TrimSpace(security.SanitizeLogLine(s))
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	// Trim again after the cut: truncation can land on (or just after) a space, and
	// every emitted string is required to be trimmed. Doing this only before the
	// cut left trailing whitespace on any clamped field.
	return strings.TrimSpace(s[:cut])
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// validSendKey is the allowlist for a synthesized answer key: exactly one of the
// digits 1-9. Screen text never reaches this field, and this is what guarantees
// it: a pane can change which options appear, but not what would be sent.
func validSendKey(s string) bool {
	return len(s) == 1 && s[0] >= '1' && s[0] <= '9'
}
