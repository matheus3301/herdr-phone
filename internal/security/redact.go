package security

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const redacted = "[REDACTED]"

// Precompiled patterns for values that must never appear in a log line, an
// audit record, or an error surfaced to a client.
var (
	// A compact JWS/JWT: three base64url segments. This is the shape of a
	// Cloudflare Access JWT.
	jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`)

	// A cloudflared tunnel token: a single dotless base64/base64url blob that
	// also begins with the `eyJ` marker ({"...) but carries no dots. Run after
	// jwtPattern so a full JWT is redacted as a whole first.
	tunnelTokenPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_\-+/=]{20,}`)

	// A pairing fragment or query parameter: pair=<base64url>.
	pairPattern = regexp.MustCompile(`(?i)\bpair=[A-Za-z0-9_\-]+`)

	// Sensitive HTTP headers, matched with the entire value to end of line so
	// multi-token values (`Bearer <tok>`) and multi-pair cookies (`a=1; b=2`)
	// are removed in full, not just up to the first space.
	headerPattern = regexp.MustCompile(`(?i)\b(cf-access-jwt-assertion|cf-access-client-secret|cf-access-client-id|cf_authorization|authorization|cookie|set-cookie|x-csrf-token)[ \t]*[:=][^\r\n]*`)
)

// RedactSecrets removes credential-like substrings from s: JWTs and tunnel
// tokens, pairing secrets, and the values of sensitive HTTP headers. It is
// conservative — it never returns a substring that matched a secret pattern —
// and is safe to apply to arbitrary text before logging. Header values are
// redacted whole (including any spaces), then any remaining JWT, dotless tunnel
// token, and pairing value is stripped.
func RedactSecrets(s string) string {
	s = headerPattern.ReplaceAllStringFunc(s, func(m string) string {
		i := strings.IndexAny(m, ":=")
		if i < 0 {
			return redacted
		}
		return m[:i+1] + " " + redacted
	})
	s = jwtPattern.ReplaceAllString(s, redacted)
	s = tunnelTokenPattern.ReplaceAllString(s, redacted)
	s = pairPattern.ReplaceAllString(s, "pair="+redacted)
	return s
}

// SanitizeLogLine strips control characters and escape sequences from s so that
// a log line can never carry an ANSI/terminal-escape injection into a terminal
// that later renders the log. It removes C0 controls (except it collapses TAB,
// CR, and LF to a single space), DEL, and C1 controls, and drops the escape
// byte and any bytes that formed part of a control sequence. Printable UTF-8 is
// preserved.
func SanitizeLogLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7F: // C0 controls and DEL (includes ESC)
			// dropped
		case r >= 0x80 && r <= 0x9F: // C1 controls
			// dropped
		case r == utf8.RuneError:
			// dropped invalid byte
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SanitizeTextBlock makes multi-line terminal-derived text safe to hand to a
// non-terminal renderer. Unlike [SanitizeLogLine] it preserves the line
// structure — LF and TAB survive — because the text is displayed as a block, not
// written to a log line. Everything that could steer a terminal or a log sink is
// removed: the escape byte, every other C0 control (CR included, so a repaint
// cannot overwrite a rendered line), DEL, C1 controls, and invalid UTF-8.
//
// This is a defence-in-depth strip, not a replacement for the terminal ANSI
// filter: it is applied to text Herdr already rendered with `format:"text"`,
// which should carry no escapes at all.
func SanitizeTextBlock(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7F: // C0 controls (incl. ESC, CR) and DEL
			// dropped
		case r >= 0x80 && r <= 0x9F: // C1 controls
			// dropped
		case r == utf8.RuneError:
			// dropped invalid byte
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SanitizeForLog makes an arbitrary string safe to write to a log or audit
// sink by redacting secrets and folding control characters.
//
// Redaction runs BEFORE newline/control folding, so it operates per original
// logical line. This matters because a sensitive-header value legitimately runs
// to the end of its line (headerPattern is [^\r\n]*): folding newlines to spaces
// first would merge separate fields onto one line and let a header value swallow
// unrelated trailing fields. Redacting first keeps each header value bounded to
// its own line; SanitizeLogLine then strips the remaining escapes/newlines.
//
// The ordering does not weaken the fail-closed guarantee — every secret pattern
// is line-bounded and removed before the text is emitted; it only prevents
// over-redacting adjacent non-secret fields on other lines.
func SanitizeForLog(s string) string {
	return SanitizeLogLine(RedactSecrets(s))
}
