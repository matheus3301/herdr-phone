package interpret

import (
	"slices"
	"strings"
)

// Numbered-option-block detection.
//
// The block is the anchor for a Claude Code interaction rather than the question
// wording, because wording changes between releases and between prompt types
// ("Do you want to proceed?", "Is this a project you created or one you trust?")
// while the shape — an ascending run of `N. label` lines — does not. Anchoring on
// shape also keeps false positives low: ordinary agent prose almost never contains
// two or more consecutive lines numbered from 1.

// selectionMarkers are the glyphs a TUI puts beside the currently highlighted row.
// They are stripped before the ordinal is read, and they are never used to infer
// *which* option is selected: on a repainted screen the marker may be stale, and
// nothing this package emits depends on it.
const selectionMarkers = "❯>▶►•*→"

// numberedOption is one parsed option line.
type numberedOption struct {
	ordinal int
	label   string
	// at is the option's index in the slice it was parsed from.
	at int
}

// parseNumberedOption reads a `N. label` or `N) label` line, tolerating a leading
// selection marker. It is a hand-rolled scan rather than a pattern so that its
// linearity is obvious and so a multi-byte marker cannot interact with a character
// class.
func parseNumberedOption(line string) (ordinal int, label string, ok bool) {
	t := strings.TrimLeft(line, " \t")
	if t == "" {
		return 0, "", false
	}
	r := []rune(t)
	i := 0
	if strings.ContainsRune(selectionMarkers, r[i]) {
		i++
		for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
			i++
		}
	}
	// Ordinal: one or two digits. Longer is not an option list, it is data.
	start := i
	for i < len(r) && i-start < 2 && r[i] >= '0' && r[i] <= '9' {
		ordinal = ordinal*10 + int(r[i]-'0')
		i++
	}
	if i == start {
		return 0, "", false
	}
	if i >= len(r) || (r[i] != '.' && r[i] != ')') {
		return 0, "", false
	}
	i++
	// A separator space is required: "1.5 seconds" must not read as option 1.
	if i >= len(r) || (r[i] != ' ' && r[i] != '\t') {
		return 0, "", false
	}
	label = strings.TrimSpace(string(r[i:]))
	if label == "" {
		return 0, "", false
	}
	return ordinal, label, true
}

// minNumberedOptions is the smallest run treated as an option list. Two is enough
// to be a decision and rare enough in prose to be safe.
const minNumberedOptions = 2

// findNumberedBlock locates the last ascending option run starting at 1.
//
// The *last* one wins: a pane's scrollback can hold several answered prompts, and
// the live one is the most recent. start and end bound the run (end exclusive).
func findNumberedBlock(lines []string) (start, end int, opts []numberedOption, ok bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		ord, label, matched := parseNumberedOption(lines[i])
		if !matched || ord != 1 {
			continue
		}
		run := []numberedOption{{ordinal: 1, label: label, at: i}}
		want := 2
		j := i + 1
		for ; j < len(lines); j++ {
			ord, label, matched := parseNumberedOption(lines[j])
			if !matched || ord != want {
				break
			}
			run = append(run, numberedOption{ordinal: ord, label: label, at: j})
			want++
		}
		if len(run) >= minNumberedOptions {
			return i, j, run, true
		}
	}
	return 0, 0, nil, false
}

// toOptions converts parsed option lines into emitted options, synthesizing each
// SendKey from the ordinal.
//
// This is the only place a send key is produced. The key is derived from the
// ordinal and validated, never copied from the label, so pane content cannot
// influence the bytes the phone would deliver. An ordinal outside 1-9 yields an
// empty key, which makes the whole interaction unanswerable rather than partly
// answerable.
func toOptions(run []numberedOption) []Option {
	out := make([]Option, 0, len(run))
	for _, o := range run {
		key := ""
		if o.ordinal >= 1 && o.ordinal <= 9 {
			key = string(rune('0' + o.ordinal))
		}
		if !validSendKey(key) {
			key = ""
		}
		out = append(out, Option{Label: o.label, SendKey: key})
	}
	return out
}

// approvalPrefixes are the option labels that mark a prompt as a permission
// decision rather than an open question. Matched on the label's first word so
// trailing qualifiers ("Yes, and always allow access to …") still classify.
var approvalPrefixes = []string{"yes", "no", "allow", "deny", "reject", "approve", "always"}

// classifyInteraction decides approval vs question from the options offered. It
// reads the options rather than the question text because the options are what the
// operator is actually being asked to choose between.
func classifyInteraction(opts []Option) InteractionKind {
	for _, o := range opts {
		first := strings.ToLower(firstWord(o.Label))
		first = strings.TrimRight(first, ",.:;")
		if slices.Contains(approvalPrefixes, first) {
			return InteractionApproval
		}
	}
	return InteractionQuestion
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}
