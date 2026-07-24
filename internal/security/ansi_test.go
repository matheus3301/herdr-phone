package security

import (
	"bytes"
	"testing"
)

// filterAll runs the whole input through a fresh filter in one call.
func filterAll(in []byte) []byte {
	return NewANSIFilter().Filter(in)
}

// filterChunked feeds the input one byte at a time to exercise fragmentation.
func filterChunked(in []byte) []byte {
	f := NewANSIFilter()
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		out = append(out, f.Filter(in[i:i+1])...)
	}
	return out
}

func TestANSI_PreservesPlainText(t *testing.T) {
	t.Parallel()
	in := []byte("hello world\nsecond line\ttabbed\r\n")
	if got := filterAll(in); !bytes.Equal(got, in) {
		t.Fatalf("plain text altered: %q", got)
	}
}

func TestANSI_PreservesUTF8(t *testing.T) {
	t.Parallel()
	in := []byte("café — 日本語 — 🚀 emoji")
	if got := filterAll(in); !bytes.Equal(got, in) {
		t.Fatalf("utf8 altered: %q", got)
	}
	// Also byte-by-byte, which splits multibyte runes across calls.
	if got := filterChunked(in); !bytes.Equal(got, in) {
		t.Fatalf("utf8 altered when chunked: %q", got)
	}
}

func TestANSI_PreservesSafeSequences(t *testing.T) {
	t.Parallel()
	esc := string(rune(0x1b))
	cases := map[string]string{
		"SGR color":       esc + "[31mred" + esc + "[0m",
		"cursor move":     esc + "[10;20H",
		"erase line":      esc + "[2K",
		"erase display":   esc + "[2J",
		"alt screen on":   esc + "[?1049h",
		"alt screen off":  esc + "[?1049l",
		"mouse tracking":  esc + "[?1000h",
		"bracketed paste": esc + "[?2004h",
		"save cursor":     esc + "7",
		"restore cursor":  esc + "8",
		"charset ascii":   esc + "(B",
		"scroll up":       esc + "[3S",
		"insert lines":    esc + "[2L",
		"scroll region":   esc + "[1;24r",
		"DECSCUSR bar":    esc + "[5 q", // CSI Ps SP q — cursor style (SP intermediate)
		"DECSCUSR block":  esc + "[2 q",
		"DECSTR reset":    esc + "[!p",           // CSI ! p — soft terminal reset
		"DECFRA fill":     esc + "[65;1;1;5;5$x", // CSI ... $ x — rect fill
		"set mode":        esc + "[4h",           // bare h/l, not a query
	}
	for name, seq := range cases {
		in := []byte("x" + seq + "y")
		if got := filterAll(in); !bytes.Equal(got, in) {
			t.Errorf("%s: sequence not preserved: in=%q out=%q", name, in, got)
		}
	}
}

func TestANSI_StripsDangerous(t *testing.T) {
	t.Parallel()
	esc := string(rune(0x1b))
	bel := string(rune(0x07))
	st := esc + `\`

	cases := map[string]string{
		"OSC 52 clipboard BEL": esc + "]52;c;aGVsbG8=" + bel,
		"OSC 52 clipboard ST":  esc + "]52;c;aGVsbG8=" + st,
		"OSC 8 hyperlink":      esc + "]8;;https://evil.example" + bel,
		"OSC 0 title":          esc + "]0;pwned" + bel,
		"OSC 1 icon":           esc + "]1;icon" + bel,
		"OSC 2 title ST":       esc + "]2;window" + st,
		"OSC 10 color query":   esc + "]10;?" + bel,
		"OSC 11 bg query":      esc + "]11;?" + st,
		"DCS DECRQSS":          esc + `P$q"q` + st,
		"APC":                  esc + "_somedata" + st,
		"PM":                   esc + "^message" + st,
		"SOS":                  esc + "Xstring" + st,
		"CSI DSR cursor":       esc + "[6n",
		"CSI DA primary":       esc + "[c",
		"CSI DA secondary":     esc + "[>c",
		"CSI XTWINOPS report":  esc + "[21t",
		"DECRQM ansi":          esc + "[?2004$p", // CSI ? Ps $ p
		"DECRQM":               esc + "[4$p",     // CSI Ps $ p
		"XTVERSION":            esc + "[>0q",     // CSI > Ps q
		"XTVERSION no param":   esc + "[>q",
		"DECREQTPARM":          esc + "[0x", // CSI Ps x
		"DECREQTPARM default":  esc + "[x",
		"DECRQCRA checksum":    esc + "[1;1;1;1;1;1*y", // CSI ... * y
		"DECID":                esc + "Z",
		"answerback ENQ":       string(rune(0x05)),
	}
	for name, seq := range cases {
		in := []byte("A" + seq + "B")
		got := filterAll(in)
		if !bytes.Equal(got, []byte("AB")) {
			t.Errorf("%s: expected sequence stripped, got %q", name, got)
		}
		// Same result when fed byte-by-byte.
		if gotC := filterChunked(in); !bytes.Equal(gotC, []byte("AB")) {
			t.Errorf("%s (chunked): expected AB, got %q", name, gotC)
		}
	}
}

func TestANSI_ChunkInvariance(t *testing.T) {
	t.Parallel()
	esc := string(rune(0x1b))
	bel := string(rune(0x07))
	in := []byte("start" + esc + "[31mcolor" + esc + "]52;c;Zm9v" + bel +
		"more" + esc + "[6n" + esc + "P$q\"q" + esc + `\` + "end")
	whole := filterAll(in)
	chunked := filterChunked(in)
	if !bytes.Equal(whole, chunked) {
		t.Fatalf("chunk invariance broken:\n whole   = %q\n chunked = %q", whole, chunked)
	}
	// The dangerous parts must be gone; the safe SGR must remain.
	if bytes.Contains(whole, []byte("52;c")) || bytes.Contains(whole, []byte("$q")) {
		t.Fatalf("dangerous content survived: %q", whole)
	}
	if !bytes.Contains(whole, []byte(esc+"[31m")) {
		t.Fatalf("SGR color was stripped: %q", whole)
	}
}

func TestANSI_FragmentedEscapeAcrossChunks(t *testing.T) {
	t.Parallel()
	esc := byte(0x1b)
	f := NewANSIFilter()
	// Feed an OSC 52 split at every boundary; nothing should leak until decided.
	parts := [][]byte{
		{esc},
		[]byte("]5"),
		[]byte("2;c;"),
		[]byte("Zm9v"),
		{0x07},
		[]byte("visible"),
	}
	var out []byte
	for _, p := range parts {
		out = append(out, f.Filter(p)...)
	}
	if string(out) != "visible" {
		t.Fatalf("fragmented OSC 52 leaked: %q", out)
	}
}

func TestANSI_IncompleteSequenceHeldBack(t *testing.T) {
	t.Parallel()
	esc := byte(0x1b)
	f := NewANSIFilter()
	// An incomplete CSI at end of stream must not be emitted.
	out := f.Filter([]byte{'a', esc, '['})
	if string(out) != "a" {
		t.Fatalf("incomplete CSI leaked: %q", out)
	}
	out = f.Filter([]byte("31m")) // completes SGR
	if string(out) != string([]byte{esc, '[', '3', '1', 'm'}) {
		t.Fatalf("completed SGR not emitted: %q", out)
	}
}

func TestANSI_BoundedRunawayOSC(t *testing.T) {
	t.Parallel()
	esc := byte(0x1b)
	f := &ANSIFilter{maxSeq: 64, maxStr: 64}
	// An OSC that never terminates and exceeds the bound is dropped; subsequent
	// ground text resumes.
	in := append([]byte{esc, ']'}, bytes.Repeat([]byte("A"), 200)...)
	f.Filter(in)
	out := f.Filter([]byte{esc, '[', '3', '1', 'm', 'x'})
	if !bytes.Contains(out, []byte("x")) {
		t.Fatalf("filter did not resynchronize after runaway OSC: %q", out)
	}
}

func TestANSI_Reset(t *testing.T) {
	t.Parallel()
	esc := byte(0x1b)
	f := NewANSIFilter()
	f.Filter([]byte{esc, '['}) // enter CSI
	f.Reset()
	out := f.Filter([]byte("plain"))
	if string(out) != "plain" {
		t.Fatalf("after Reset expected plain, got %q", out)
	}
}

func TestANSI_Writer(t *testing.T) {
	t.Parallel()
	esc := string(rune(0x1b))
	bel := string(rune(0x07))
	var buf bytes.Buffer
	w := NewANSIWriter(&buf)
	n, err := w.Write([]byte("A" + esc + "]52;c;Zm9v" + bel + "B"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Write reports the full input length consumed.
	if n == 0 {
		t.Fatal("Write returned 0")
	}
	if buf.String() != "AB" {
		t.Fatalf("writer output = %q, want AB", buf.String())
	}
}

func TestANSI_Idempotent(t *testing.T) {
	t.Parallel()
	esc := string(rune(0x1b))
	bel := string(rune(0x07))
	in := []byte("x" + esc + "]0;title" + bel + esc + "[31mred" + esc + "[6n")
	once := filterAll(in)
	twice := filterAll(once)
	if !bytes.Equal(once, twice) {
		t.Fatalf("filter not idempotent:\n once=%q\n twice=%q", once, twice)
	}
}

func TestANSI_Strips8bitC1(t *testing.T) {
	t.Parallel()
	const (
		DCS = 0x90
		SOS = 0x98
		CSI = 0x9B
		ST  = 0x9C
		OSC = 0x9D
		PM  = 0x9E
		APC = 0x9F
	)
	cases := map[string][]byte{
		"8-bit CSI DSR":      {'A', CSI, '6', 'n', 'B'},
		"8-bit CSI DA":       {'A', CSI, 'c', 'B'},
		"8-bit OSC 52 (8ST)": {'A', OSC, '5', '2', ';', 'c', ';', 'Z', 'm', '9', 'v', ST, 'B'},
		"8-bit OSC title":    {'A', OSC, '0', ';', 'x', ST, 'B'},
		"8-bit DCS":          {'A', DCS, 'x', 'y', 'z', ST, 'B'},
		"8-bit APC":          {'A', APC, 'd', 'a', 't', 'a', ST, 'B'},
		"8-bit PM":           {'A', PM, 'm', ST, 'B'},
		"8-bit SOS":          {'A', SOS, 's', ST, 'B'},
		"lone ST":            {'A', ST, 'B'},
		"lone C1 IND":        {'A', 0x84, 'B'},
		"lone C1 NEL":        {'A', 0x85, 'B'},
	}
	for name, in := range cases {
		if got := filterAll(in); !bytes.Equal(got, []byte("AB")) {
			t.Errorf("%s: expected AB, got %q", name, got)
		}
		if got := filterChunked(in); !bytes.Equal(got, []byte("AB")) {
			t.Errorf("%s (chunked): expected AB, got %q", name, got)
		}
	}
}

func TestANSI_8bitCSIPreservedAsNormalized(t *testing.T) {
	t.Parallel()
	// An 8-bit CSI rendering sequence is preserved but normalized to 7-bit ESC [.
	esc := byte(0x1b)
	in := []byte{'A', 0x9B, '3', '1', 'm', 'B'}
	got := filterAll(in)
	want := []byte{'A', esc, '[', '3', '1', 'm', 'B'}
	if !bytes.Equal(got, want) {
		t.Fatalf("8-bit SGR normalization: got %q, want %q", got, want)
	}
	if bytes.IndexByte(got, 0x9B) >= 0 {
		t.Fatal("raw 8-bit C1 introducer survived in output")
	}
}

func TestANSI_UTF8ContinuationBytesPreserved(t *testing.T) {
	t.Parallel()
	// 'Û' (U+00DB) is 0xC3 0x9B in UTF-8: its continuation byte 0x9B collides
	// with the 8-bit CSI introducer but must be forwarded intact.
	in := []byte{'A', 0xC3, 0x9B, 'B'} // "AÛB"
	if got := filterAll(in); !bytes.Equal(got, in) {
		t.Fatalf("utf8 with C1-valued continuation altered: got %q, want %q", got, in)
	}
	if got := filterChunked(in); !bytes.Equal(got, in) {
		t.Fatalf("utf8 (chunked) altered: got %q, want %q", got, in)
	}
	// A 4-byte emoji whose bytes include values in the C1 range.
	rocket := []byte("🚀") // F0 9F 9A 80 — 0x9F/0x9A are continuation bytes
	full := append([]byte("A"), rocket...)
	full = append(full, 'B')
	if got := filterAll(full); !bytes.Equal(got, full) {
		t.Fatalf("emoji altered: got %q, want %q", got, full)
	}
}
