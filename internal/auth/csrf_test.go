package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidCSRF(t *testing.T) {
	t.Parallel()
	if !ValidCSRF("token123", "token123") {
		t.Fatal("matching tokens should validate")
	}
	if ValidCSRF("token123", "token124") {
		t.Fatal("mismatched tokens should not validate")
	}
	if ValidCSRF("", "") {
		t.Fatal("empty tokens must never validate")
	}
	if ValidCSRF("token", "") || ValidCSRF("", "token") {
		t.Fatal("empty side must never validate")
	}
}

func TestValidateRequestCSRF(t *testing.T) {
	t.Parallel()
	store := NewSessionStore(time.Hour, 0)
	sess, _ := store.Create(Identity{Email: "op@example.com"}, time.Time{})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/mutations", nil)
	r.Header.Set(CSRFHeader, sess.CSRFToken)
	if !store.ValidateRequestCSRF(r, sess.ID) {
		t.Fatal("correct CSRF header should validate")
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/v1/mutations", nil)
	bad.Header.Set(CSRFHeader, "wrong")
	if store.ValidateRequestCSRF(bad, sess.ID) {
		t.Fatal("wrong CSRF header must not validate")
	}

	// Unknown session fails closed.
	if store.ValidateRequestCSRF(r, "nonexistent") {
		t.Fatal("unknown session must not validate")
	}
}

func TestSessionIDFromRequest(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := SessionIDFromRequest(r); got != "" {
		t.Fatalf("no cookie should yield empty, got %q", got)
	}
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "abc123"})
	if got := SessionIDFromRequest(r); got != "abc123" {
		t.Fatalf("SessionIDFromRequest = %q", got)
	}
}
