package interpret

import (
	"strings"
	"testing"
	"time"
)

// Worst-case work, measured rather than assumed.
//
// findNumberedBlock scans backwards for an option run starting at 1 and, for each
// candidate, scans forward while the ordinals ascend. An input crafted out of many
// overlapping ascending sequences therefore makes the forward scans overlap, which
// is the shape most likely to turn a linear pass into a quadratic one.
//
// Limits.MaxLines caps the input at 2000 lines regardless, so the real question is
// not the asymptotics but whether the bounded worst case is fast enough to sit on a
// request path. These tests pin that.

// adversarialOptionLines builds overlapping ascending runs: 1,1,2,1,2,3,1,2,3,4...
// Every "1." is a candidate block start and every candidate scans forward.
func adversarialOptionLines(lines int) string {
	var b strings.Builder
	n := 1
	for b.Len() < lines*8 {
		for i := 1; i <= n; i++ {
			b.WriteString(" ")
			b.WriteString(itoa(i))
			b.WriteString(". option text\n")
		}
		n++
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestBoundedWorkOnAdversarialOptionBlocks(t *testing.T) {
	input := adversarialOptionLines(4000) // deliberately over MaxLines
	lim := DefaultLimits()

	start := time.Now()
	for range 20 {
		for _, kind := range ParserKinds() {
			Parse(kind, input, lim)
		}
	}
	elapsed := time.Since(start)

	// 40 parses of a maximally awkward, over-length input. A generous ceiling: the
	// point is to catch an accidental blow-up, not to benchmark.
	if elapsed > 2*time.Second {
		t.Errorf("40 adversarial parses took %s; this sits on a request path", elapsed)
	}
	t.Logf("40 adversarial parses of a %d-byte input in %s", len(input), elapsed)
}

// MaxLines must actually bound the scan: an enormous pane read cannot make the
// parser proportional to the whole scrollback.
func TestMaxLinesBoundsTheScan(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxLines = 50

	// 5000 marked lines, of which only the last 50 may be examined.
	var b strings.Builder
	for i := range 5000 {
		b.WriteString("⏺ turn ")
		b.WriteString(itoa(i))
		b.WriteString("\n\n")
	}

	res, ok := Parse(KindClaude, b.String(), lim)
	if !ok {
		t.Fatal("expected a match")
	}
	// Every turn comes from the tail, so the newest content survives and the oldest
	// is never even read.
	if len(res.Turns) > lim.MaxLines {
		t.Errorf("turns = %d, cannot exceed the %d-line scan bound", len(res.Turns), lim.MaxLines)
	}
	last := res.Turns[len(res.Turns)-1]
	if !strings.Contains(last.Text, "4999") {
		t.Errorf("last turn = %q, want the newest output to survive", last.Text)
	}
}

// The total emitted text stays proportional to the input, not to
// MaxTurns × MaxTextLen: each input line lands in at most one turn.
func TestEmittedTextIsProportionalToInput(t *testing.T) {
	input := strings.Repeat("⏺ some agent prose on one line\n\n", 200)
	res, ok := Parse(KindClaude, input, DefaultLimits())
	if !ok {
		t.Fatal("expected a match")
	}
	total := 0
	for _, turn := range res.Turns {
		total += len(turn.Text) + len(turn.Tool)
	}
	if total > len(input) {
		t.Errorf("emitted %d bytes from a %d-byte input; output must not amplify", total, len(input))
	}
}
