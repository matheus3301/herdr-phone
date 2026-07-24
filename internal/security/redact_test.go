package security

import (
	"strings"
	"testing"
)

func TestRedactSecrets_JWT(t *testing.T) {
	t.Parallel()
	jwt := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.c2lnbmF0dXJlLWJ5dGVz"
	in := "authenticated with token " + jwt + " ok"
	out := RedactSecrets(in)
	if strings.Contains(out, jwt) {
		t.Fatalf("JWT survived redaction: %q", out)
	}
	if !strings.Contains(out, redacted) {
		t.Fatalf("expected redaction marker: %q", out)
	}
}

func TestRedactSecrets_PairingFragment(t *testing.T) {
	t.Parallel()
	in := "https://host/#pair=abcDEF123_-secretvalue"
	out := RedactSecrets(in)
	if strings.Contains(out, "abcDEF123_-secretvalue") {
		t.Fatalf("pairing secret survived: %q", out)
	}
}

func TestRedactSecrets_Headers(t *testing.T) {
	t.Parallel()
	// Each case pairs an input header line with the secret substrings that must
	// be entirely absent from the output — including tokens after a space and
	// every value in a multi-pair cookie.
	cases := []struct {
		in      string
		secrets []string
	}{
		{"Cf-Access-Jwt-Assertion: eyJabc.def.ghi", []string{"eyJabc.def.ghi"}},
		{"Cookie: __Host-herdr_phone=deadbeef; other=cafe", []string{"deadbeef", "cafe"}},
		{"Authorization: Bearer sometoken", []string{"sometoken", "Bearer"}},
		{"CF-Access-Client-Secret: supersecretvalue", []string{"supersecretvalue"}},
		{"X-CSRF-Token: csrftoken123", []string{"csrftoken123"}},
		{"set-cookie: __Host-herdr_phone=abc123; Path=/; Secure", []string{"abc123"}},
	}
	for _, tc := range cases {
		out := RedactSecrets(tc.in)
		if !strings.Contains(out, redacted) {
			t.Errorf("header value not redacted: %q -> %q", tc.in, out)
		}
		for _, secret := range tc.secrets {
			if strings.Contains(out, secret) {
				t.Errorf("secret %q survived: %q -> %q", secret, tc.in, out)
			}
		}
	}
}

func TestRedactSecrets_DotlessTunnelToken(t *testing.T) {
	t.Parallel()
	// A cloudflared tunnel token is a single dotless base64 blob beginning eyJ.
	token := "eyJaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	in := "starting cloudflared with token " + token + " now"
	out := RedactSecrets(in)
	if strings.Contains(out, token) {
		t.Fatalf("dotless tunnel token survived: %q", out)
	}
	if !strings.Contains(out, redacted) {
		t.Fatalf("expected redaction marker: %q", out)
	}
}

func TestRedactSecrets_FullAuthorizationValue(t *testing.T) {
	t.Parallel()
	// Regression for the \S+ leak: the entire value after "Bearer" must go.
	in := "Authorization: Bearer abc.def.ghi trailing words"
	out := RedactSecrets(in)
	for _, s := range []string{"abc.def.ghi", "Bearer"} {
		if strings.Contains(out, s) {
			t.Fatalf("value %q survived full-value redaction: %q", s, out)
		}
	}
}

func TestSanitizeLogLine(t *testing.T) {
	t.Parallel()
	esc := string(rune(0x1b))
	bel := string(rune(0x07))
	in := "user output" + esc + "]0;pwned" + bel + esc + "[31mred" + "\x00\x07 more\ttab\nnewline"
	out := SanitizeLogLine(in)
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("escape survived: %q", out)
	}
	if strings.ContainsRune(out, 0x00) || strings.ContainsRune(out, 0x07) {
		t.Fatalf("control char survived: %q", out)
	}
	// Tabs and newlines are collapsed to spaces, text preserved.
	if !strings.Contains(out, "user output") || !strings.Contains(out, "more") {
		t.Fatalf("text lost: %q", out)
	}
}

func TestSanitizeLogLine_KeepsUTF8(t *testing.T) {
	t.Parallel()
	in := "café 日本語 🚀"
	if out := SanitizeLogLine(in); out != in {
		t.Fatalf("utf8 altered: %q", out)
	}
}

func TestSanitizeForLog(t *testing.T) {
	t.Parallel()
	esc := string(rune(0x1b))
	jwt := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.c2ln"
	in := esc + "[31m" + "token=" + jwt
	out := SanitizeForLog(in)
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("escape survived: %q", out)
	}
	if strings.Contains(out, jwt) {
		t.Fatalf("jwt survived: %q", out)
	}
}

// TestSanitizeForLog_MultilineNoOverRedaction verifies the R5/N3 fix: redaction
// runs per original logical line (before newline folding), so a sensitive header
// on one line does not swallow unrelated non-secret fields on other lines, while
// the secret is still fully removed (fail-closed).
func TestSanitizeForLog_MultilineNoOverRedaction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		in       string
		secrets  []string // must be absent from output
		preserve []string // must survive on their own line
	}{
		{
			name:     "authorization then request id",
			in:       "Authorization: Bearer secrettok123\nrequest_id=abc-123\nstatus=200",
			secrets:  []string{"secrettok123", "Bearer"},
			preserve: []string{"request_id=abc-123", "status=200"},
		},
		{
			name:     "cookie between plain fields",
			in:       "method=POST\nCookie: __Host-herdr_phone=deadbeef; other=cafe\npath=/api/v1/mutations",
			secrets:  []string{"deadbeef", "cafe"},
			preserve: []string{"method=POST", "path=/api/v1/mutations"},
		},
		{
			name:     "CRLF separated header and field",
			in:       "Authorization: Bearer crlftok\r\nuser=operator@example.com",
			secrets:  []string{"crlftok"},
			preserve: []string{"user=operator@example.com"},
		},
		{
			name:     "trailing field on same logical line stays only if newline separated",
			in:       "set-cookie: __Host-herdr_phone=xyz789; Path=/\nregion=us-east",
			secrets:  []string{"xyz789"},
			preserve: []string{"region=us-east"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := SanitizeForLog(tc.in)
			for _, s := range tc.secrets {
				if strings.Contains(out, s) {
					t.Errorf("secret %q survived: %q -> %q", s, tc.in, out)
				}
			}
			for _, p := range tc.preserve {
				if !strings.Contains(out, p) {
					t.Errorf("non-secret field %q was over-redacted: %q -> %q", p, tc.in, out)
				}
			}
			if !strings.Contains(out, redacted) {
				t.Errorf("expected redaction marker: %q -> %q", tc.in, out)
			}
		})
	}
}

// TestSanitizeForLog_SingleLineStillRedactsWholeValue confirms that on a single
// logical line the header value is still fully removed (no regression from the
// ordering change).
func TestSanitizeForLog_SingleLineStillRedactsWholeValue(t *testing.T) {
	t.Parallel()
	out := SanitizeForLog("Authorization: Bearer abc.def.ghi extra bits")
	for _, s := range []string{"abc.def.ghi", "Bearer", "extra bits"} {
		if strings.Contains(out, s) {
			t.Fatalf("value %q survived on single-line header: %q", s, out)
		}
	}
}

// TestSanitizeTextBlock covers the multi-line sanitizer used for observed
// terminal output: line structure survives, every terminal control does not.
func TestSanitizeTextBlock(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"keeps newlines and tabs", "a\nb\tc", "a\nb\tc"},
		{"drops escape", "a\x1b[31mb", "a[31mb"},
		{"drops osc title", "a\x1b]0;pwned\x07b", "a]0;pwnedb"},
		{"drops carriage return", "typed\rrewritten", "typedrewritten"},
		{"drops nul and del", "a\x00b\x7fc", "abc"},
		{"drops c1", "abc", "abc"},
		{"keeps utf8", "héllo 日本 ✓", "héllo 日本 ✓"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeTextBlock(tc.in); got != tc.want {
				t.Fatalf("SanitizeTextBlock(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// No control character other than LF and TAB may survive. A carriage return in
// particular must go: it would let a repaint overwrite a line the operator has
// already read in a non-terminal renderer.
func TestSanitizeTextBlockNoControlSurvives(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for r := rune(0); r < 0xA0; r++ {
		b.WriteRune(r)
	}
	out := SanitizeTextBlock(b.String())
	for _, r := range out {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			t.Fatalf("control %U survived sanitization", r)
		}
	}
	if !strings.Contains(out, "\n") || !strings.Contains(out, "\t") {
		t.Fatalf("newline and tab must survive: %q", out)
	}
}
