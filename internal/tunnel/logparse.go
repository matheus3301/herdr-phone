package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// maxLogLineBytes bounds the sanitized length retained for any single log line,
// protecting the ring buffer and any downstream logging from unbounded input.
const maxLogLineBytes = 2 * 1024

// quickURLRe matches a Quick Tunnel hostname. It is deliberately strict: only
// lowercase host labels under trycloudflare.com over https.
var quickURLRe = regexp.MustCompile(`https://[a-z0-9][a-z0-9-]*\.trycloudflare\.com`)

// connRegisteredMarkers are substrings (matched case-insensitively) that
// indicate cloudflared has established at least one edge connection. For named
// tunnels this is the readiness signal.
var connRegisteredMarkers = []string{
	"registered tunnel connection",
	"connection registered",
	"registered connection",
}

// LogLine is a parsed, sanitized cloudflared log record. Raw is always populated
// and safe to store; the structured fields are best-effort.
type LogLine struct {
	Level   string
	Message string
	Time    string
	Raw     string
}

type eventKind int

const (
	evOther eventKind = iota
	evQuickURL
	evConnRegistered
	evError
)

type logEvent struct {
	kind eventKind
	url  string
	line LogLine
}

// parseLogLine turns one raw cloudflared output line into a LogLine. It attempts
// to decode `--output json` records ({"level","message"|"msg","time"}) and falls
// back to treating the whole line as an opaque message. All fields are
// sanitized and bounded.
func parseLogLine(b []byte) LogLine {
	raw := sanitizeLogText(string(b))
	line := LogLine{Raw: raw}

	trimmed := strings.TrimSpace(string(b))
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var rec struct {
			Level   string `json:"level"`
			Message string `json:"message"`
			Msg     string `json:"msg"`
			Time    string `json:"time"`
		}
		if err := json.Unmarshal([]byte(trimmed), &rec); err == nil {
			msg := rec.Message
			if msg == "" {
				msg = rec.Msg
			}
			line.Level = sanitizeLogText(rec.Level)
			line.Message = sanitizeLogText(msg)
			line.Time = sanitizeLogText(rec.Time)
		}
	}
	return line
}

// classify inspects a parsed line and returns its lifecycle meaning.
func classify(line LogLine) logEvent {
	haystacks := []string{line.Message, line.Raw}

	for _, h := range haystacks {
		if m := quickURLRe.FindString(strings.ToLower(h)); m != "" {
			return logEvent{kind: evQuickURL, url: m, line: line}
		}
	}

	for _, h := range haystacks {
		lower := strings.ToLower(h)
		for _, marker := range connRegisteredMarkers {
			if strings.Contains(lower, marker) {
				return logEvent{kind: evConnRegistered, line: line}
			}
		}
	}

	if strings.EqualFold(line.Level, "error") || strings.EqualFold(line.Level, "fatal") {
		return logEvent{kind: evError, line: line}
	}
	return logEvent{kind: evOther, line: line}
}

// sanitizeLogText removes control characters (which could corrupt terminals or
// smuggle escape sequences) and bounds the length.
func sanitizeLogText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Drop CR/LF and other control characters entirely.
			continue
		default:
			b.WriteRune(r)
		}
		if b.Len() >= maxLogLineBytes {
			break
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// redactExecError strips any argv the os/exec error may echo so a token command
// argv (which could reference secret-bearing tooling) never lands in an error
// string. Only a generic cause is preserved.
func redactExecError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("exited with status %d", exitErr.ExitCode())
	}
	var pathErr *exec.Error
	if errors.As(err, &pathErr) {
		return fmt.Errorf("command not executable: %w", pathErr.Err)
	}
	return err
}
