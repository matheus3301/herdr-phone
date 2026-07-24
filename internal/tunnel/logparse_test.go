package tunnel

import (
	"strings"
	"testing"
)

func TestParseLogLineJSON(t *testing.T) {
	t.Parallel()
	line := parseLogLine([]byte(`{"level":"info","message":"Registered tunnel connection connIndex=0","time":"2026-07-23T10:00:00Z"}`))
	if line.Level != "info" {
		t.Errorf("level = %q", line.Level)
	}
	if !strings.Contains(line.Message, "Registered tunnel connection") {
		t.Errorf("message = %q", line.Message)
	}
	if classify(line).kind != evConnRegistered {
		t.Errorf("expected evConnRegistered")
	}
}

func TestParseLogLineJSONMsgField(t *testing.T) {
	t.Parallel()
	line := parseLogLine([]byte(`{"level":"info","msg":"hello","time":"t"}`))
	if line.Message != "hello" {
		t.Errorf("message from msg field = %q", line.Message)
	}
}

func TestParseLogLineNonJSON(t *testing.T) {
	t.Parallel()
	line := parseLogLine([]byte("plain text log with no json"))
	if line.Raw == "" {
		t.Error("raw should be populated for non-json")
	}
	if line.Message != "" {
		t.Errorf("message should be empty for non-json, got %q", line.Message)
	}
}

func TestQuickURLExtraction(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"level":"info","message":"Your quick Tunnel has been created! Visit it at https://calm-forest-1234.trycloudflare.com","time":"t"}`,
		`+--------------------------------------+ https://calm-forest-1234.trycloudflare.com +---+`,
	}
	for _, raw := range cases {
		ev := classify(parseLogLine([]byte(raw)))
		if ev.kind != evQuickURL {
			t.Fatalf("expected quick url event for %q, got kind %d", raw, ev.kind)
		}
		if ev.url != "https://calm-forest-1234.trycloudflare.com" {
			t.Errorf("url = %q", ev.url)
		}
	}
}

func TestQuickURLRejectsNonTrycloudflare(t *testing.T) {
	t.Parallel()
	ev := classify(parseLogLine([]byte(`{"message":"visit https://evil.example.com now"}`))) //nolint
	if ev.kind == evQuickURL {
		t.Errorf("must not treat non-trycloudflare host as quick url")
	}
}

func TestClassifyError(t *testing.T) {
	t.Parallel()
	ev := classify(parseLogLine([]byte(`{"level":"error","message":"failed to connect"}`)))
	if ev.kind != evError {
		t.Errorf("expected evError, got %d", ev.kind)
	}
}

func TestSanitizeLogTextStripsControl(t *testing.T) {
	t.Parallel()
	in := "line\x1b[31mred\x00\r\nnext\ttab"
	out := sanitizeLogText(in)
	if strings.ContainsAny(out, "\x00\r\n\x1b") {
		t.Errorf("control chars not stripped: %q", out)
	}
	if !strings.Contains(out, "tab") {
		t.Errorf("tab text lost: %q", out)
	}
}

func TestSanitizeLogTextBounded(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("a", maxLogLineBytes*4)
	out := sanitizeLogText(in)
	if len(out) > maxLogLineBytes {
		t.Errorf("sanitized length %d exceeds bound %d", len(out), maxLogLineBytes)
	}
}
