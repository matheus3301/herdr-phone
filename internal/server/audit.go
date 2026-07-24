package server

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/matheus3301/herdr-phone/internal/security"
)

// maxAuditFieldLen bounds any single audit string field so a hostile id cannot
// bloat the log.
const maxAuditFieldLen = 512

// FileAuditor appends sanitized audit records as JSON lines to a mode-0600 file.
// Every structural mutation and terminal lifecycle event is recorded; terminal
// content, commands, JWTs, cookies, and pairing values never are (section 17).
type FileAuditor struct {
	mu  sync.Mutex
	f   *os.File
	now func() time.Time
	enc *json.Encoder
}

// NewFileAuditor opens (creating if needed) the audit file with mode 0600 for
// append. now may be nil (defaults to time.Now).
func NewFileAuditor(path string, now func() time.Time) (*FileAuditor, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	// Enforce 0600 even if the file pre-existed with looser bits.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &FileAuditor{f: f, now: now, enc: json.NewEncoder(f)}, nil
}

// Record writes one sanitized entry. It stamps the time if unset and never
// blocks the caller for I/O errors (they are dropped; audit is best effort but
// the file is fsync-free append).
func (a *FileAuditor) Record(e AuditEntry) {
	if e.Time.IsZero() {
		e.Time = a.now()
	}
	e = sanitizeEntry(e)
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.enc.Encode(e)
}

// Close closes the underlying file.
func (a *FileAuditor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f.Close()
}

// sanitizeEntry strips control characters and bounds the length of every string
// field, so a crafted id or detail cannot inject newlines/escapes into the log.
func sanitizeEntry(e AuditEntry) AuditEntry {
	e.Event = sanitizeField(e.Event)
	e.Subject = sanitizeField(e.Subject)
	e.SessionID = sanitizeField(e.SessionID)
	e.Operation = sanitizeField(e.Operation)
	e.Resource = sanitizeField(e.Resource)
	e.RequestID = sanitizeField(e.RequestID)
	e.Result = sanitizeField(e.Result)
	e.Detail = sanitizeField(e.Detail)
	return e
}

// sanitizeField makes a field safe to persist. It first routes the value through
// the shared security sanitizer, which redacts credential-like substrings
// (Authorization/Cookie headers, JWTs, cloudflared tunnel tokens, pairing
// values) and strips control/escape sequences and invalid UTF-8. It then applies
// the audit sink's own control stripping as defense in depth and bounds the
// length so a hostile field cannot bloat the log.
func sanitizeField(s string) string {
	if s == "" {
		return s
	}
	s = security.SanitizeForLog(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == 0x1b || r < 0x20 || r == 0x7f {
			// Drop C0 controls, DEL, and ESC entirely.
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > maxAuditFieldLen {
		out = out[:maxAuditFieldLen]
	}
	return out
}

// nopAuditor discards records. It is the default when no auditor is injected.
type nopAuditor struct{}

func (nopAuditor) Record(AuditEntry) {}
