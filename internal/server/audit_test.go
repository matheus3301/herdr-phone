package server

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFileAuditorWritesSanitized0600(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/audit.jsonl"
	now := time.Unix(1_700_000_000, 0).UTC()

	a, err := NewFileAuditor(path, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFileAuditor: %v", err)
	}
	defer a.Close()

	// An entry with control characters and an ANSI escape in fields, including
	// Detail, which carries attacker-influenced terminal close reasons.
	a.Record(AuditEntry{
		Event:     "mutation",
		Operation: "pane.close\x1b[31m",
		Resource:  "pane\n1\tX",
		Detail:    "reason\x1b]0;evil\x07\r\ninjected",
		Result:    "ok",
	})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("audit file perm = %o, want 600", perm)
	}

	f, _ := os.Open(path)
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	if strings.ContainsAny(line, "\x1b") {
		t.Error("audit line contains ESC")
	}

	var e AuditEntry
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatalf("audit line not valid JSON: %v", err)
	}
	if strings.Contains(e.Operation, "\x1b") {
		t.Errorf("operation not sanitized: %q", e.Operation)
	}
	// Newline and tab (control chars) must be gone from resource; the shared
	// sanitizer collapses them to spaces.
	if strings.ContainsAny(e.Resource, "\n\t") {
		t.Errorf("resource not sanitized: %q", e.Resource)
	}
	if e.Resource != "pane 1 X" {
		t.Errorf("resource = %q, want %q", e.Resource, "pane 1 X")
	}
	// Detail must be routed through the sanitizer: no ESC, BEL, CR, or LF.
	if strings.ContainsAny(e.Detail, "\x1b\x07\r\n") {
		t.Errorf("detail not sanitized: %q", e.Detail)
	}
	if !e.Time.Equal(now) {
		t.Errorf("time = %v, want %v", e.Time, now)
	}
}

func TestFileAuditorRedactsCredentials(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/audit.jsonl"
	a, err := NewFileAuditor(path, func() time.Time { return time.Unix(1_700_000_000, 0) })
	if err != nil {
		t.Fatalf("NewFileAuditor: %v", err)
	}
	defer a.Close()

	// Each secret is placed in a field and must never survive to disk. The
	// substrings below are the sensitive parts that must be gone.
	secrets := []string{
		"SECRET_AUTH_abc123",          // Authorization value
		"SECRET_COOKIE_val",           // Cookie value
		"SECRETJWTSIG",                // JWT signature segment
		"SECRETPAIRING123",            // pairing secret
		"SECRETTUNNELTOKENabcdefghij", // cloudflared tunnel token body
	}
	a.Record(AuditEntry{Event: "e1", Operation: "Authorization: Bearer SECRET_AUTH_abc123"})
	a.Record(AuditEntry{Event: "e2", Detail: "Cookie: __Host-herdr_phone=SECRET_COOKIE_val; other=2"})
	a.Record(AuditEntry{Event: "e3", Detail: "token eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhYmMifQ.SECRETJWTSIG trailer"})
	a.Record(AuditEntry{Event: "e4", Detail: "link https://h/#pair=SECRETPAIRING123 done"})
	a.Record(AuditEntry{Event: "e5", Detail: "tok eyJSECRETTUNNELTOKENabcdefghijklmnopqrstuvwxyz end"})
	// A non-secret SessionID audit handle must be preserved (M2 contract).
	a.Record(AuditEntry{Event: "e6", SessionID: "sid-audit-handle"})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blob := string(raw)

	for _, secret := range secrets {
		if strings.Contains(blob, secret) {
			t.Errorf("audit log leaked secret substring %q", secret)
		}
	}
	if !strings.Contains(blob, "[REDACTED]") {
		t.Error("expected redaction marker in audit log")
	}
	if !strings.Contains(blob, "sid-audit-handle") {
		t.Error("non-secret SessionID audit handle must be preserved")
	}
}

func TestSanitizeFieldTruncates(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", maxAuditFieldLen+100)
	got := sanitizeField(long)
	if len(got) != maxAuditFieldLen {
		t.Fatalf("len = %d, want %d", len(got), maxAuditFieldLen)
	}
}
