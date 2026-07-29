package interpret

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/matheus3301/herdr-phone/internal/security"
)

// FuzzParse is the oracle for the interpretation pass, in the same spirit as the
// ANSI filter's fuzz oracle in internal/security. The parser reads untrusted pane
// bytes, so the properties asserted here are the ones the rest of the system is
// allowed to assume — extend this alongside any grammar change.
//
// The invariants are:
//
//  1. Parse never panics, for any input, for any parser.
//  2. Every emitted string is sanitized: no C0/C1 controls, no newlines, valid
//     UTF-8 with no replacement runes introduced by a mid-rune cut.
//  3. Every emitted string respects Limits.MaxTextLen, and every collection
//     respects its bound.
//  4. Option.SendKey is always either empty or a single digit 1-9 — pane content
//     can never place arbitrary bytes in the field the phone would send.
//  5. Answerable implies every option carries a valid key.
//  6. ok == false implies a zero-value Result, so a caller that ignores ok cannot
//     accidentally render a partial reading.
func FuzzParse(f *testing.F) {
	f.Add("")
	f.Add("⏺ hello")
	f.Add("⏺ Bash(echo hi)\n  ⎿  hi\n")
	f.Add(" Do you want to proceed?\n ❯ 1. Yes\n   2. No\n")
	f.Add("  ┃  △ Permission required\n  ┃    → Edit x\n  ┃   Allow once   Reject\n")
	f.Add("1. \x00\x01\x02\n2. \x1b[31mred\x1b[0m\n")
	f.Add(strings.Repeat("─", 500))
	f.Add("⏺ " + strings.Repeat("é", 5000))
	f.Add("\n\n\n\n")
	f.Add("❯ 9. nine\n❯ 1. one\n2. two\n")

	lim := Limits{
		MaxTurns:     8,
		MaxOptions:   4,
		MaxDetail:    3,
		MaxDiffLines: 5,
		MaxTextLen:   64,
		MaxLines:     200,
	}

	f.Fuzz(func(t *testing.T, in string) {
		for _, kind := range ParserKinds() {
			res, ok := Parse(kind, in, lim)

			if !ok {
				// Property 6.
				if len(res.Turns) != 0 || res.Interaction != nil || res.Parser != "" {
					t.Fatalf("kind=%s: ok=false but result is non-zero: %#v", kind, res)
				}
				continue
			}

			if res.Parser != kind {
				t.Fatalf("kind=%s: result parser = %q", kind, res.Parser)
			}
			// Property 3, collections.
			if len(res.Turns) > lim.MaxTurns {
				t.Fatalf("kind=%s: %d turns over bound %d", kind, len(res.Turns), lim.MaxTurns)
			}

			for _, turn := range res.Turns {
				checkString(t, kind, "turn.Text", turn.Text, lim.MaxTextLen)
				checkString(t, kind, "turn.Tool", turn.Tool, maxToolNameLen)
				switch turn.Kind {
				case TurnAgentText, TurnToolCall, TurnToolResult, TurnStatus:
				default:
					t.Fatalf("kind=%s: unknown turn kind %q", kind, turn.Kind)
				}
				if turn.Tool != "" && turn.Kind != TurnToolCall {
					t.Fatalf("kind=%s: tool name on a %s turn", kind, turn.Kind)
				}
			}

			in := res.Interaction
			if in == nil {
				continue
			}
			switch in.Kind {
			case InteractionApproval, InteractionQuestion:
			default:
				t.Fatalf("kind=%s: unknown interaction kind %q", kind, in.Kind)
			}
			checkString(t, kind, "interaction.Title", in.Title, lim.MaxTextLen)
			checkString(t, kind, "interaction.Question", in.Question, lim.MaxTextLen)

			if len(in.Detail) > lim.MaxDetail {
				t.Fatalf("kind=%s: %d detail lines over bound %d", kind, len(in.Detail), lim.MaxDetail)
			}
			for _, d := range in.Detail {
				checkString(t, kind, "interaction.Detail", d, lim.MaxTextLen)
			}

			if len(in.Options) > lim.MaxOptions {
				t.Fatalf("kind=%s: %d options over bound %d", kind, len(in.Options), lim.MaxOptions)
			}
			for _, o := range in.Options {
				checkString(t, kind, "option.Label", o.Label, maxOptionLabelLen)
				// Property 4.
				if o.SendKey != "" && !validSendKey(o.SendKey) {
					t.Fatalf("kind=%s: option %q carries invalid send key %q", kind, o.Label, o.SendKey)
				}
				// Property 5.
				if in.Answerable && o.SendKey == "" {
					t.Fatalf("kind=%s: answerable interaction has keyless option %q", kind, o.Label)
				}
			}
			if in.Answerable && len(in.Options) == 0 {
				t.Fatalf("kind=%s: answerable interaction with no options", kind)
			}
			// OpenCode prompts can never be answerable, whatever the input claims.
			if kind == KindOpenCode && in.Answerable {
				t.Fatalf("kind=%s: OpenCode interaction reported answerable", kind)
			}

			if len(in.Diff) > lim.MaxDiffLines {
				t.Fatalf("kind=%s: %d diff lines over bound %d", kind, len(in.Diff), lim.MaxDiffLines)
			}
			for _, d := range in.Diff {
				checkString(t, kind, "diff.Text", d.Text, lim.MaxTextLen)
				switch d.Op {
				case DiffContext, DiffAdd, DiffRemove:
				default:
					t.Fatalf("kind=%s: unknown diff op %q", kind, d.Op)
				}
				if d.Line < 0 {
					t.Fatalf("kind=%s: negative diff line %d", kind, d.Line)
				}
			}
		}
	})
}

// checkString asserts property 2 and property 3 for one emitted field.
func checkString(t *testing.T, kind Kind, field, s string, maxLen int) {
	t.Helper()
	if s == "" {
		return
	}
	if len(s) > maxLen {
		t.Fatalf("kind=%s: %s is %d bytes, over bound %d: %q", kind, field, len(s), maxLen, s)
	}
	if !utf8.ValidString(s) {
		t.Fatalf("kind=%s: %s is not valid UTF-8: %q", kind, field, s)
	}
	if got := security.SanitizeLogLine(s); got != s {
		t.Fatalf("kind=%s: %s is not sanitized: %q became %q", kind, field, s, got)
	}
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			t.Fatalf("kind=%s: %s contains a raw control %q: %q", kind, field, r, s)
		}
		if r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			t.Fatalf("kind=%s: %s contains control %U: %q", kind, field, r, s)
		}
	}
	if strings.TrimSpace(s) != s {
		t.Fatalf("kind=%s: %s is not trimmed: %q", kind, field, s)
	}
}
