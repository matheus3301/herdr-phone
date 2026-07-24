// Package security implements the origin-side defensive controls for the
// herdr-phone relay that are shared across packages: secret and
// control-character redaction for log/audit sinks, and a streaming ANSI
// terminal filter for controller output.
//
// None of the code here trusts a network peer. Every helper fails closed: on
// any ambiguity a secret is redacted rather than preserved, and a terminal
// control sequence is dropped rather than forwarded. (Request-level controls —
// middleware, security headers, Host/Origin checks, and bounded bodies — live
// in internal/server, which owns HTTP routing and enforces them there.)
package security

import (
	"bytes"
	"io"
)

// Control bytes recognized by the ANSI filter.
const (
	ctrlENQ byte = 0x05 // answerback trigger
	ctrlBEL byte = 0x07 // OSC/string terminator (BEL form)
	ctrlESC byte = 0x1B // escape
)

// 8-bit C1 control introducers. In UTF-8 these byte values only ever appear as
// continuation bytes inside a multibyte rune; a "lone" C1 byte (one that is not
// an expected continuation) is a genuine C1 control and is neutralized. String
// introducers (DCS/SOS/PM/APC) are stripped; CSI/OSC are normalized to their
// 7-bit ESC forms and re-parsed so the same strip rules apply.
const (
	c1DCS byte = 0x90 // Device Control String
	c1SOS byte = 0x98 // Start Of String
	c1CSI byte = 0x9B // Control Sequence Introducer
	c1ST  byte = 0x9C // String Terminator
	c1OSC byte = 0x9D // Operating System Command
	c1PM  byte = 0x9E // Privacy Message
	c1APC byte = 0x9F // Application Program Command
)

// Buffer bounds for in-progress control sequences. CSI and OSC sequences are
// always short in practice; any sequence that grows past these limits without
// terminating is treated as hostile or corrupt and dropped. String controls
// (DCS/APC/PM/SOS) are stripped entirely, so we only need to scan for their
// terminator up to a generous ceiling before resynchronizing to ground.
const (
	defaultMaxSeqBytes = 4096
	defaultMaxStrBytes = 1 << 20
)

// ansiState is the current position of the streaming parser.
type ansiState int

const (
	stGround ansiState = iota
	stEsc              // saw ESC
	stEscInt           // ESC + one or more intermediate bytes (charset, etc.)
	stCSI              // ESC [ ... collecting until a final byte
	stOSC              // ESC ] ... collecting until BEL or ST
	stOSCEsc           // inside OSC, saw ESC (candidate ST)
	stStr              // DCS/APC/PM/SOS string, always stripped
	stStrEsc           // inside a string control, saw ESC (candidate ST)
)

// ANSIFilter removes dangerous terminal control sequences from a byte stream
// before it reaches a browser terminal emulator (xterm.js), the operator's real
// terminal, or a log sink. It is a stateful, streaming parser: input may be
// delivered in arbitrary fragments and a control sequence may straddle any
// number of Filter calls. The filtered output for a given byte stream is
// identical regardless of how that stream is chunked.
//
// Stripped:
//   - answerback (ENQ, 0x05)
//   - CSI device/status queries and reports that induce a terminal reply:
//     DSR ("n"), DA ("c"), XTWINOPS/title reporting ("t"), DECRQM ("$ p"),
//     XTVERSION ("> q"), DECREQTPARM ("x"), and DECRQCRA ("* y")
//   - ESC Z (DECID device identification query)
//   - OSC 0/1/2 (window/icon title), OSC 8 (hyperlinks), OSC 52 (clipboard),
//     and any OSC color query (payload containing '?')
//   - every DCS, APC, PM and SOS string control
//   - lone 8-bit C1 controls (0x80–0x9F), with the CSI/OSC/DCS/PM/APC/SOS
//     introducers neutralized rather than forwarded
//
// Preserved:
//   - SGR colors and styling, cursor movement, erasure, scrolling
//   - DEC private modes: alternate screen, mouse tracking, bracketed paste
//   - DECSCUSR ("SP q") cursor shape, DECSTR ("! p") soft reset, and
//     rectangular-area ops such as DECFRA ("$ x")
//   - charset designation and other 2-byte ESC sequences
//   - UTF-8 text and ordinary C0 controls (TAB, LF, CR, BS, BEL)
//
// UTF-8 is tracked byte-accurately: a lead byte reserves its continuation
// bytes, which are always passed through even when their value collides with a
// C1 introducer, so a multibyte rune split across a chunk boundary is forwarded
// intact while a genuine lone C1 control is still neutralized.
type ANSIFilter struct {
	state         ansiState
	seq           []byte // buffered in-progress sequence, including its introducer
	strN          int    // length scanned while dropping a string control
	utf8Remaining int    // UTF-8 continuation bytes still expected in ground state
	maxSeq        int
	maxStr        int
}

// NewANSIFilter returns a filter positioned at ground state.
func NewANSIFilter() *ANSIFilter {
	return &ANSIFilter{maxSeq: defaultMaxSeqBytes, maxStr: defaultMaxStrBytes}
}

// Reset returns the filter to ground state, discarding any in-progress sequence.
func (f *ANSIFilter) Reset() {
	f.state = stGround
	f.seq = f.seq[:0]
	f.strN = 0
	f.utf8Remaining = 0
}

// utf8Continuation returns the number of continuation bytes that follow a UTF-8
// lead byte, or 0 for a byte that cannot begin a valid multibyte sequence.
func utf8Continuation(lead byte) int {
	switch {
	case lead >= 0xF0 && lead <= 0xF7:
		return 3
	case lead >= 0xE0 && lead <= 0xEF:
		return 2
	case lead >= 0xC0 && lead <= 0xDF:
		return 1
	default:
		return 0
	}
}

// Filter consumes p and returns the sanitized bytes produced by it. Bytes that
// belong to an incomplete control sequence at the end of p are retained
// internally and emitted (or dropped) once the sequence completes on a later
// call.
func (f *ANSIFilter) Filter(p []byte) []byte {
	if f.maxSeq == 0 {
		f.maxSeq = defaultMaxSeqBytes
	}
	if f.maxStr == 0 {
		f.maxStr = defaultMaxStrBytes
	}
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		b := p[i]
		switch f.state {
		case stGround:
			// Pass through expected UTF-8 continuation bytes verbatim, even when
			// their value collides with a C1 introducer.
			if f.utf8Remaining > 0 {
				if b >= 0x80 && b <= 0xBF {
					out = append(out, b)
					f.utf8Remaining--
					continue
				}
				// Not a continuation byte: the pending lead was malformed.
				f.utf8Remaining = 0
			}
			switch {
			case b == ctrlESC:
				f.state = stEsc
				f.seq = append(f.seq[:0], b)
			case b == ctrlENQ:
				// drop answerback trigger
			case b == c1CSI:
				// 8-bit CSI: normalize to the 7-bit ESC [ form and re-parse.
				f.state = stCSI
				f.seq = append(f.seq[:0], ctrlESC, '[')
			case b == c1OSC:
				// 8-bit OSC: normalize to the 7-bit ESC ] form and re-parse.
				f.state = stOSC
				f.seq = append(f.seq[:0], ctrlESC, ']')
			case b == c1DCS || b == c1SOS || b == c1PM || b == c1APC:
				// 8-bit DCS/SOS/PM/APC string controls: always stripped.
				f.state = stStr
				f.strN = 0
				f.seq = f.seq[:0]
			case b == c1ST:
				// lone 8-bit String Terminator: drop.
			case b >= 0x80 && b <= 0x9F:
				// any other lone C1 control (not a UTF-8 continuation): drop.
			case b >= 0xC0:
				// UTF-8 lead byte: emit and reserve its continuation bytes.
				out = append(out, b)
				f.utf8Remaining = utf8Continuation(b)
			default:
				// printable ASCII, C0 controls (TAB/LF/CR/BS/BEL...), and stray
				// 0xA0-0xBF bytes with no lead: pass through.
				out = append(out, b)
			}

		case stEsc:
			switch {
			case b == '[':
				f.state = stCSI
				f.seq = append(f.seq, b)
			case b == ']':
				f.state = stOSC
				f.seq = append(f.seq, b)
			case b == 'P' || b == '_' || b == '^' || b == 'X':
				// DCS / APC / PM / SOS: always stripped.
				f.state = stStr
				f.strN = 0
				f.seq = f.seq[:0]
			case b == 'Z':
				// DECID device query: drop ESC Z entirely.
				f.state = stGround
				f.seq = f.seq[:0]
			case b == ctrlESC:
				// restart escape
				f.seq = append(f.seq[:0], b)
			case b >= 0x20 && b <= 0x2F:
				f.state = stEscInt
				f.seq = append(f.seq, b)
			case b >= 0x30 && b <= 0x7E:
				// complete 2-byte ESC sequence (RIS, save/restore cursor,
				// keypad mode, index, etc.) — pass through.
				f.seq = append(f.seq, b)
				out = append(out, f.seq...)
				f.state = stGround
				f.seq = f.seq[:0]
			default:
				// C0 control (CAN/SUB/...) aborts the escape.
				f.state = stGround
				f.seq = f.seq[:0]
				i--
			}

		case stEscInt:
			switch {
			case b >= 0x20 && b <= 0x2F:
				f.seq = append(f.seq, b)
			case b >= 0x30 && b <= 0x7E:
				f.seq = append(f.seq, b)
				out = append(out, f.seq...)
				f.state = stGround
				f.seq = f.seq[:0]
			case b == ctrlESC:
				f.seq = append(f.seq[:0], b)
				f.state = stEsc
			default:
				f.state = stGround
				f.seq = f.seq[:0]
				i--
			}
			if len(f.seq) > f.maxSeq {
				f.state = stGround
				f.seq = f.seq[:0]
			}

		case stCSI:
			switch {
			case b >= 0x30 && b <= 0x3F: // parameter bytes: 0-9 : ; < = > ?
				f.seq = append(f.seq, b)
			case b >= 0x20 && b <= 0x2F: // intermediate bytes
				f.seq = append(f.seq, b)
			case b >= 0x40 && b <= 0x7E: // final byte
				f.seq = append(f.seq, b)
				if !csiShouldStrip(f.seq) {
					out = append(out, f.seq...)
				}
				f.state = stGround
				f.seq = f.seq[:0]
			case b == ctrlESC:
				f.seq = append(f.seq[:0], b)
				f.state = stEsc
			default:
				f.state = stGround
				f.seq = f.seq[:0]
				i--
			}
			if len(f.seq) > f.maxSeq {
				f.state = stGround
				f.seq = f.seq[:0]
			}

		case stOSC:
			switch {
			case b == ctrlBEL || b == c1ST:
				f.seq = append(f.seq, ctrlBEL)
				if !oscShouldStrip(f.seq) {
					out = append(out, f.seq...)
				}
				f.state = stGround
				f.seq = f.seq[:0]
			case b == ctrlESC:
				f.state = stOSCEsc
			case b < 0x20 || b == 0x7F || (b >= 0x80 && b <= 0x9F):
				// Any C0/C1 control (other than the BEL/ST terminators and ESC
				// handled above) cancels the OSC string per ECMA-48. Drop the
				// buffer and reinterpret this byte from ground so neither an
				// answerback control nor a raw 8-bit introducer can leak inside
				// an emitted OSC payload.
				f.state = stGround
				f.seq = f.seq[:0]
				i--
			default:
				// Accumulate only printable (0x20-0x7E) and high (0xA0-0xFF)
				// bytes into the payload.
				f.seq = append(f.seq, b)
				if len(f.seq) > f.maxSeq {
					f.state = stGround
					f.seq = f.seq[:0]
				}
			}

		case stOSCEsc:
			if b == '\\' { // ST
				f.seq = append(f.seq, ctrlESC, '\\')
				if !oscShouldStrip(f.seq) {
					out = append(out, f.seq...)
				}
				f.state = stGround
				f.seq = f.seq[:0]
			} else {
				// Malformed OSC: drop it and reprocess this byte from ground.
				f.state = stGround
				f.seq = f.seq[:0]
				i--
			}

		case stStr:
			switch {
			case b == ctrlBEL || b == c1ST:
				f.state = stGround
				f.strN = 0
			case b == ctrlESC:
				f.state = stStrEsc
			default:
				f.strN++
				if f.strN > f.maxStr {
					f.state = stGround
					f.strN = 0
				}
			}

		case stStrEsc:
			// String controls are dropped regardless of terminator; if this ESC
			// was not the ST intro, reprocess the byte from ground.
			f.state = stGround
			f.strN = 0
			if b != '\\' {
				i--
			}
		}
	}
	return out
}

// csiShouldStrip reports whether a complete CSI sequence (from its ESC [ or
// normalized 8-bit introducer through the final byte) must be dropped. Only
// sequences that induce a terminal reply are stripped; rendering sequences —
// SGR, cursor movement, erasure, DEC private modes, DECSCUSR, DECSTR,
// rectangular-area ops — are preserved. The whole buffered sequence is inspected
// so the private prefix and intermediate bytes can disambiguate a query from a
// look-alike rendering sequence sharing the same final byte.
func csiShouldStrip(seq []byte) bool {
	body := seq
	if len(body) >= 2 && body[0] == ctrlESC && body[1] == '[' {
		body = body[2:]
	}
	if len(body) == 0 {
		return false
	}
	final := body[len(body)-1]
	mid := body[:len(body)-1]                   // private-prefix + parameter + intermediate bytes
	hasDollar := bytes.IndexByte(mid, '$') >= 0 // 0x24 intermediate
	hasStar := bytes.IndexByte(mid, '*') >= 0   // 0x2A intermediate
	hasSpace := bytes.IndexByte(mid, ' ') >= 0  // 0x20 (SP) intermediate
	hasGT := bytes.IndexByte(mid, '>') >= 0     // 0x3E private prefix

	switch final {
	case 'n', // DSR: device status report / cursor position query
		'c', // DA: primary/secondary/tertiary device attributes query
		't': // XTWINOPS: window manipulation and title/size reporting
		return true
	case 'p':
		// DECRQM request mode ("CSI [?]Ps $ p"); preserve DECSTR ("CSI ! p").
		return hasDollar
	case 'q':
		// XTVERSION ("CSI > q"); preserve DECSCUSR ("CSI Ps SP q").
		return hasGT && !hasSpace
	case 'x':
		// DECREQTPARM ("CSI Ps x"); preserve DECFRA ("CSI ... $ x").
		return !hasDollar
	case 'y':
		// DECRQCRA checksum request ("CSI ... * y").
		return hasStar
	}
	return false
}

// oscShouldStrip reports whether a complete OSC sequence (introducer through
// terminator) must be dropped.
func oscShouldStrip(seq []byte) bool {
	p := seq
	if len(p) >= 2 && p[0] == ctrlESC && p[1] == ']' {
		p = p[2:]
	}
	// Trim the terminator (BEL or ST).
	if n := len(p); n > 0 && p[n-1] == ctrlBEL {
		p = p[:n-1]
	} else if n := len(p); n >= 2 && p[n-2] == ctrlESC && p[n-1] == '\\' {
		p = p[:n-2]
	}

	code := -1
	j := 0
	for j < len(p) && p[j] >= '0' && p[j] <= '9' {
		if code < 0 {
			code = 0
		}
		code = code*10 + int(p[j]-'0')
		if code > 1_000_000 { // avoid overflow on absurd input
			break
		}
		j++
	}

	switch code {
	case 0, 1, 2: // window / icon title
		return true
	case 8: // hyperlink
		return true
	case 52: // clipboard read/write
		return true
	}

	// Any OSC carrying a '?' is a color/state query that induces a reply.
	for _, c := range p {
		if c == '?' {
			return true
		}
	}
	return false
}

// ansiWriter adapts an ANSIFilter to io.Writer, filtering every write before
// forwarding it to the wrapped writer.
type ansiWriter struct {
	w io.Writer
	f *ANSIFilter
}

// NewANSIWriter returns an io.Writer that filters all bytes written to it and
// forwards the sanitized result to w. The returned writer is not safe for
// concurrent use.
func NewANSIWriter(w io.Writer) io.Writer {
	return &ansiWriter{w: w, f: NewANSIFilter()}
}

func (a *ansiWriter) Write(p []byte) (int, error) {
	filtered := a.f.Filter(p)
	if len(filtered) == 0 {
		return len(p), nil
	}
	if _, err := a.w.Write(filtered); err != nil {
		return 0, err
	}
	return len(p), nil
}
