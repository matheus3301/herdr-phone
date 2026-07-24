package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestSession_CreateAndGet(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := NewSessionStore(time.Hour, 0, WithSessionClock(func() time.Time { return now }))
	sess, err := store.Create(Identity{Email: "op@example.com"}, time.Time{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(sess.ID) < 40 {
		t.Fatalf("session id too short: %q", sess.ID)
	}
	if sess.CSRFToken == "" || sess.CSRFToken == sess.ID {
		t.Fatal("csrf token must be present and distinct from id")
	}
	got, ok := store.Get(sess.ID)
	if !ok {
		t.Fatal("Get should find the session")
	}
	if got.Identity.Email != "op@example.com" {
		t.Fatalf("identity = %q", got.Identity.Email)
	}
}

func TestSession_HardExpiryFromJWT(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := NewSessionStore(12*time.Hour, 0, WithSessionClock(func() time.Time { return now }))
	// JWT expires in 5 minutes, sooner than the 12h TTL.
	hard := now.Add(5 * time.Minute)
	sess, err := store.Create(Identity{Email: "op@example.com"}, hard)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !sess.ExpiresAt.Equal(hard) {
		t.Fatalf("ExpiresAt = %v, want %v (JWT expiry)", sess.ExpiresAt, hard)
	}
}

func TestSession_AbsoluteExpiry(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	store := NewSessionStore(time.Hour, 0, WithSessionClock(clock))
	sess, _ := store.Create(Identity{Email: "op@example.com"}, time.Time{})

	now = time.Unix(1000, 0).Add(2 * time.Hour)
	if _, ok := store.Get(sess.ID); ok {
		t.Fatal("expired session must not be returned")
	}
	if store.Len() != 0 {
		t.Fatal("expired session should be evicted on Get")
	}
}

func TestSession_IdleExpiry(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	store := NewSessionStore(12*time.Hour, 30*time.Minute, WithSessionClock(clock))
	sess, _ := store.Create(Identity{Email: "op@example.com"}, time.Time{})

	// Within idle window: refreshes LastSeen.
	now = time.Unix(1000, 0).Add(20 * time.Minute)
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("session within idle window should be live")
	}
	// 20 more minutes since last access is still under 30m idle.
	now = time.Unix(1000, 0).Add(40 * time.Minute)
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("idle timer should reset on access")
	}
	// Now exceed the idle window.
	now = time.Unix(1000, 0).Add(40*time.Minute + 31*time.Minute)
	if _, ok := store.Get(sess.ID); ok {
		t.Fatal("session past idle window should expire")
	}
}

func TestSession_Delete(t *testing.T) {
	t.Parallel()
	store := NewSessionStore(time.Hour, 0)
	sess, _ := store.Create(Identity{Email: "op@example.com"}, time.Time{})
	store.Delete(sess.ID)
	if _, ok := store.Get(sess.ID); ok {
		t.Fatal("deleted session must not be found")
	}
}

func TestSession_GC(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	store := NewSessionStore(time.Hour, 0, WithSessionClock(clock))
	_, _ = store.Create(Identity{Email: "a@example.com"}, time.Time{})
	_, _ = store.Create(Identity{Email: "b@example.com"}, time.Time{})

	now = time.Unix(1000, 0).Add(2 * time.Hour)
	if n := store.GC(); n != 2 {
		t.Fatalf("GC removed %d, want 2", n)
	}
	if store.Len() != 0 {
		t.Fatal("store should be empty after GC")
	}
}

func TestSession_UniqueIDs(t *testing.T) {
	t.Parallel()
	store := NewSessionStore(time.Hour, 0)
	seenID := map[string]bool{}
	seenAudit := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, err := store.Create(Identity{Email: "op@example.com"}, time.Time{})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if seenID[s.ID] {
			t.Fatal("duplicate session id")
		}
		if seenAudit[s.AuditID] {
			t.Fatal("duplicate audit id")
		}
		seenID[s.ID] = true
		seenAudit[s.AuditID] = true
	}
}

func TestSession_AuditIDIsDistinctNonSecret(t *testing.T) {
	t.Parallel()
	store := NewSessionStore(time.Hour, 0)
	sess, err := store.Create(Identity{Email: "op@example.com"}, time.Time{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.AuditID == "" {
		t.Fatal("AuditID must be populated")
	}
	// The audit label must NOT equal the bearer secret (ID) or the CSRF token.
	if sess.AuditID == sess.ID {
		t.Fatal("AuditID must differ from the session bearer (ID)")
	}
	if sess.AuditID == sess.CSRFToken {
		t.Fatal("AuditID must differ from the CSRF token")
	}
	// The audit label must NOT be usable as a session identifier — it is not a
	// credential and can be safely written to audit logs.
	if _, ok := store.Get(sess.AuditID); ok {
		t.Fatal("AuditID must not resolve to a session (it is not a credential)")
	}
	// The audit label is stable across lookups of the same session.
	got, ok := store.Get(sess.ID)
	if !ok {
		t.Fatal("session should resolve by its ID")
	}
	if got.AuditID != sess.AuditID {
		t.Fatalf("AuditID changed across lookups: %q != %q", got.AuditID, sess.AuditID)
	}
}

func TestSession_JanitorEvictsExpired(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	store := NewSessionStore(time.Hour, 0, WithSessionClock(clock))
	if _, err := store.Create(Identity{Email: "op@example.com"}, time.Time{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("Len = %d, want 1", store.Len())
	}

	// Expire the session, then let the janitor evict it.
	now = time.Unix(1000, 0).Add(2 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.StartJanitor(ctx, 10*time.Millisecond) // clamped up to minJanitorInterval

	deadline := time.Now().Add(3 * time.Second)
	for store.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("janitor did not evict expired session in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSession_JanitorStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	store := NewSessionStore(time.Hour, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		store.RunJanitor(ctx, time.Minute)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunJanitor did not return after context cancel")
	}
}

func TestSessionCookie_Attributes(t *testing.T) {
	t.Parallel()
	c := NewSessionCookie("value", time.Unix(9999, 0))
	if c.Name != CookieName {
		t.Fatalf("name = %q, want %q", c.Name, CookieName)
	}
	if !c.HttpOnly || !c.Secure {
		t.Fatal("cookie must be HttpOnly and Secure")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie must be SameSite=Strict")
	}
	if c.Path != "/" {
		t.Fatal("__Host- cookie must have Path=/")
	}
	if c.Domain != "" {
		t.Fatal("__Host- cookie must not set a Domain")
	}
}

func TestClearSessionCookie(t *testing.T) {
	t.Parallel()
	c := ClearSessionCookie()
	if c.MaxAge >= 0 {
		t.Fatal("clear cookie must have negative MaxAge")
	}
	if c.Name != CookieName {
		t.Fatalf("name = %q", c.Name)
	}
}

func TestIdentity_Display(t *testing.T) {
	t.Parallel()
	if got := (Identity{Quick: true}).Display(); got != "Quick Tunnel operator" {
		t.Fatalf("quick display = %q", got)
	}
	if got := (Identity{Email: "a@b.com"}).Display(); got != "a@b.com" {
		t.Fatalf("email display = %q", got)
	}
	if got := (Identity{CommonName: "svc"}).Display(); got != "svc" {
		t.Fatalf("cn display = %q", got)
	}
}
