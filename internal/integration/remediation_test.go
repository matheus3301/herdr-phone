package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/auth"
)

func newQuickAuthAdapter() *authAdapter {
	pairing, _ := auth.NewPairing()
	return &authAdapter{
		named:    false,
		pairing:  pairing,
		sessions: auth.NewSessionStore(time.Hour, 0),
		baseURL:  func() string { return "https://phone.example.com" },
	}
}

// TestSessionIDIsAuditIDNotBearerCookie proves the identity the server records in
// audit logs is the non-secret AuditID, never the bearer cookie value.
func TestSessionIDIsAuditIDNotBearerCookie(t *testing.T) {
	t.Parallel()
	ad := newQuickAuthAdapter()
	secret := ad.pairing.Token()

	sess, err := ad.Pair(httptest.NewRequest("POST", "/api/v1/pair", nil), secret)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	cookie := sess.Cookie.Value
	if cookie == "" {
		t.Fatal("expected a bearer cookie value")
	}
	if sess.Identity.SessionID == "" {
		t.Fatal("expected a non-empty SessionID")
	}
	if sess.Identity.SessionID == cookie {
		t.Fatal("bearer cookie id leaked into Identity.SessionID (must be the AuditID)")
	}

	// Resolving by cookie (as the middleware does) yields the same non-secret
	// audit id, never the bearer value.
	id, ok := ad.Session(cookie)
	if !ok {
		t.Fatal("Session should resolve the cookie")
	}
	if id.SessionID != sess.Identity.SessionID {
		t.Errorf("Session().SessionID = %q, want %q", id.SessionID, sess.Identity.SessionID)
	}
	if id.SessionID == cookie {
		t.Errorf("Session().SessionID must not be the bearer cookie value")
	}
}

// TestCSRFTokenSurvivesReload proves that after a browser reload (only the
// HttpOnly cookie persists), the session and its CSRF token remain valid and are
// recoverable for continued mutations without re-pairing.
func TestCSRFTokenSurvivesReload(t *testing.T) {
	t.Parallel()
	ad := newQuickAuthAdapter()
	secret := ad.pairing.Token()

	sess, err := ad.Pair(httptest.NewRequest("POST", "/api/v1/pair", nil), secret)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	cookie := sess.Cookie.Value
	origCSRF := sess.CSRFToken

	// Simulate a reload: the SPA lost its in-memory CSRF token; only the cookie
	// remains. Resolving the session (as GET /session does) recovers the CSRF
	// token and expiry from the identity.
	id, ok := ad.Session(cookie)
	if !ok {
		t.Fatal("session should survive a reload")
	}
	if id.SessionID != sess.Identity.SessionID {
		t.Errorf("audit id changed across reload: %q vs %q", id.SessionID, sess.Identity.SessionID)
	}
	if id.CSRFToken != origCSRF {
		t.Errorf("CSRF token changed across reload: %q vs %q", id.CSRFToken, origCSRF)
	}
	if !id.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Errorf("expiry changed across reload: %v vs %v", id.ExpiresAt, sess.ExpiresAt)
	}
	// The recovered CSRF token still authorizes a mutating request.
	if !ad.ValidateCSRF(cookie, id.CSRFToken) {
		t.Error("recovered CSRF token must validate after reload")
	}
}

// TestDispatchRejectsUnknownFields proves mutation params are decoded strictly:
// unknown or non-canonical fields are rejected before any Herdr call.
func TestDispatchRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		op     string
		params string
		method string // herdr method that must NOT be called
	}{
		{"agent.prompt with legacy target", "agent.prompt", `{"pane_id":"p1","text":"hi","target":"x"}`, "agent.prompt"},
		{"pane.focus with extra field", "pane.focus", `{"pane_id":"p1","extra":1}`, "pane.focus"},
		{"worktree.remove with non-canonical workspace_id", "worktree.remove", `{"workspace_id":"ws"}`, "worktree.remove"},
		{"agent.send_keys with junk", "agent.send_keys", `{"pane_id":"p1","keys":["enter"],"junk":true}`, "agent.send_keys"},
		{"pane.split with legacy target_pane_id", "pane.split", `{"pane_id":"p1","direction":"right","target_pane_id":"p2"}`, "pane.split"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeHerdr(t)
			c := dispatchClientFor(f)
			_, err := dispatchMutation(context.Background(), c, tc.op, json.RawMessage(tc.params))
			if err == nil {
				t.Fatalf("expected strict-decode rejection for %s", tc.params)
			}
			if got := f.params(tc.method); got != nil {
				t.Errorf("herdr %q must not be called on a strict-decode failure", tc.method)
			}
		})
	}
}

// TestDispatchAcceptsCanonicalWorktreeRemove confirms the canonical worktree_id
// field (the open workspace id) is accepted and forwarded as Herdr's workspace_id.
func TestDispatchAcceptsCanonicalWorktreeRemove(t *testing.T) {
	t.Parallel()
	f := startFakeHerdr(t)
	c := dispatchClientFor(f)
	if _, err := dispatchMutation(context.Background(), c, "worktree.remove", json.RawMessage(`{"worktree_id":"wsX"}`)); err != nil {
		t.Fatalf("canonical worktree.remove should succeed: %v", err)
	}
	got := f.params("worktree.remove")
	if got == nil {
		t.Fatal("herdr worktree.remove should have been called")
	}
	var m struct {
		WorkspaceID string `json:"workspace_id"`
	}
	_ = json.Unmarshal(got, &m)
	if m.WorkspaceID != "wsX" {
		t.Errorf("worktree_id should map to Herdr workspace_id, got %q", m.WorkspaceID)
	}
}
