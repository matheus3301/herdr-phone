package security

import (
	"bytes"
	"testing"
)

// containsForbidden reports whether out contains a control sequence the filter
// is required to remove. It is a conservative structural scan used to catch
// regressions where a dangerous sequence survives filtering.
func containsForbidden(out []byte) (string, bool) {
	if bytes.IndexByte(out, ctrlENQ) >= 0 {
		return "answerback ENQ", true
	}
	if b, bad := containsLoneC1(out); bad {
		return "lone 8-bit C1 introducer 0x" + string("0123456789abcdef"[b>>4]) + string("0123456789abcdef"[b&0xf]), true
	}
	for i := 0; i+1 < len(out); i++ {
		if out[i] != ctrlESC {
			continue
		}
		switch out[i+1] {
		case 'P', '_', '^', 'X':
			return "DCS/APC/PM/SOS introducer", true
		case 'Z':
			return "DECID", true
		case ']':
			// Inspect the OSC that follows; if it is a stripped code it must
			// not appear in the output.
			if oscForbiddenAt(out, i) {
				return "forbidden OSC", true
			}
		case '[':
			if csiForbiddenAt(out, i) {
				return "device-query CSI", true
			}
		}
	}
	return "", false
}

// containsLoneC1 reports whether out contains an 8-bit C1 string/CSI/OSC
// introducer as a standalone control — one that is not reserved as a
// continuation byte by a preceding UTF-8 lead byte. It mirrors the filter's own
// UTF-8 accounting so that a continuation byte of a (possibly incomplete)
// multibyte rune whose value falls in the C1 range is not mistaken for a lone
// introducer.
func containsLoneC1(out []byte) (byte, bool) {
	remaining := 0
	for i := 0; i < len(out); i++ {
		b := out[i]
		if remaining > 0 {
			if b >= 0x80 && b <= 0xBF { // expected continuation byte
				remaining--
				continue
			}
			remaining = 0 // malformed; reinterpret b fresh below
		}
		if b >= 0xC0 { // UTF-8 lead byte (0xF8-0xFF reserve none)
			remaining = utf8Continuation(b)
			continue
		}
		if b >= 0x80 && b <= 0x9F {
			switch b {
			case c1DCS, c1SOS, c1CSI, c1ST, c1OSC, c1PM, c1APC:
				return b, true
			}
		}
	}
	return 0, false
}

// oscForbiddenAt checks the OSC beginning at out[i] (ESC ]) for a stripped code
// or a query payload. It mirrors oscShouldStrip, including the overflow cap on
// the numeric code so an absurdly long code cannot wrap into a stripped value.
func oscForbiddenAt(out []byte, i int) bool {
	j := i + 2
	code := -1
	for j < len(out) && out[j] >= '0' && out[j] <= '9' {
		if code < 0 {
			code = 0
		}
		code = code*10 + int(out[j]-'0')
		if code > 1_000_000 { // same cap as oscShouldStrip
			break
		}
		j++
	}
	switch code {
	case 0, 1, 2, 8, 52:
		return true
	}
	// Any OSC carrying a '?' before its terminator is a query and is stripped.
	for k := i + 2; k < len(out); k++ {
		c := out[k]
		if c == ctrlBEL || c == ctrlESC {
			break
		}
		if c == '?' {
			return true
		}
	}
	return false
}

// csiForbiddenAt checks the CSI beginning at out[i] (ESC [) for a reply-inducing
// device query. It mirrors csiShouldStrip: DSR/DA/XTWINOPS (n/c/t), DECRQM
// ("$ p"), XTVERSION ("> q"), DECREQTPARM (bare "x"), and DECRQCRA ("* y").
func csiForbiddenAt(out []byte, i int) bool {
	j := i + 2
	for j < len(out) {
		b := out[j]
		if b >= 0x40 && b <= 0x7e { // final byte
			mid := out[i+2 : j]
			switch b {
			case 'n', 'c', 't':
				return true
			case 'p':
				return bytes.IndexByte(mid, '$') >= 0
			case 'q':
				return bytes.IndexByte(mid, '>') >= 0 && bytes.IndexByte(mid, ' ') < 0
			case 'x':
				return bytes.IndexByte(mid, '$') < 0
			case 'y':
				return bytes.IndexByte(mid, '*') >= 0
			}
			return false
		}
		if !((b >= 0x30 && b <= 0x3f) || (b >= 0x20 && b <= 0x2f)) {
			return false // malformed; not a complete CSI
		}
		j++
	}
	return false
}

// FuzzANSIFilter enforces four invariants over arbitrary input:
//  1. filtering is invariant to how the input is chunked;
//  2. the output never contains a sequence the filter must strip, including
//     reply-inducing CSI queries and lone 8-bit C1 introducers;
//  3. the filter is idempotent;
//  4. output length is bounded (8-bit→7-bit normalization grows by ≤1 byte per
//     introducer, so at most 2× input).
func FuzzANSIFilter(f *testing.F) {
	esc := string(rune(0x1b))
	bel := string(rune(0x07))
	seeds := [][]byte{
		[]byte(""),
		[]byte("plain text"),
		[]byte(esc + "[31mcolor" + esc + "[0m"),
		[]byte(esc + "]52;c;Zm9v" + bel),
		[]byte(esc + "]8;;http://x" + bel),
		[]byte(esc + "P$q\"q" + esc + `\`),
		[]byte(esc + "[6n" + esc + "[c" + esc + "[21t"),
		[]byte(esc + "[?2004$p" + esc + "[>0q" + esc + "[0x" + esc + "[1;1;1;1;1;1*y"), // DECRQM/XTVERSION/DECREQTPARM/DECRQCRA
		[]byte(esc + "[5 q" + esc + "[!p" + esc + "[65;1;1;5;5$x"),                     // DECSCUSR/DECSTR/DECFRA (preserved)
		[]byte(esc + "Z" + string(rune(0x05))),
		[]byte("café 🚀 日本語"),
		{'A', 0x9b, '6', 'n', 'B'},                           // 8-bit CSI DSR
		{'A', 0x9d, '5', '2', ';', 'c', ';', 'x', 0x9c, 'B'}, // 8-bit OSC 52
		{'A', 0x90, 'd', 'a', 't', 'a', 0x9c, 'B'},           // 8-bit DCS
		{'A', 0xC3, 0x9B, 'B'},                               // UTF-8 'Û' with C1-valued continuation
		[]byte(esc + "["),                                    // incomplete
		[]byte(esc + "]0;abc"),                               // unterminated OSC
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		whole := NewANSIFilter().Filter(in)

		// (1) Chunk invariance: byte-by-byte must equal the single-shot result.
		fc := NewANSIFilter()
		var chunked []byte
		for i := 0; i < len(in); i++ {
			chunked = append(chunked, fc.Filter(in[i:i+1])...)
		}
		if !bytes.Equal(whole, chunked) {
			t.Fatalf("chunk variance:\n whole=%q\n chunked=%q\n in=%q", whole, chunked, in)
		}

		// (2) No forbidden sequence survives.
		if what, bad := containsForbidden(whole); bad {
			t.Fatalf("forbidden content survived (%s): out=%q in=%q", what, whole, in)
		}

		// (3) Idempotence.
		twice := NewANSIFilter().Filter(whole)
		if !bytes.Equal(whole, twice) {
			t.Fatalf("not idempotent:\n once=%q\n twice=%q\n in=%q", whole, twice, in)
		}

		// Output growth is bounded: the only source of growth is normalizing an
		// 8-bit CSI/OSC introducer (1 byte) to its 7-bit ESC form (2 bytes), so
		// output is at most twice the input plus a small constant.
		if len(whole) > 2*len(in)+2 {
			t.Fatalf("output grew beyond bound: %d > 2*%d+2", len(whole), len(in))
		}
	})
}
