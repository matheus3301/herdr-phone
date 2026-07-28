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

func TestIdentitySubject_ReuseKey(t *testing.T) {
	t.Parallel()
	if got := IdentitySubject(Identity{Email: "op@example.com", CommonName: "svc"}); got != "op@example.com" {
		t.Errorf("email must win the key: %q", got)
	}
	if got := IdentitySubject(Identity{CommonName: "svc"}); got != "svc" {
		t.Errorf("common_name key = %q", got)
	}
	// A quick identity and an identity with no claim are never reusable.
	if got := IdentitySubject(Identity{Quick: true}); got != "" {
		t.Errorf("quick identity key = %q, want empty", got)
	}
	if got := IdentitySubject(Identity{Email: "op@example.com", Quick: true}); got != "" {
		t.Errorf("quick identity key = %q, want empty even with an email", got)
	}
	if got := IdentitySubject(Identity{}); got != "" {
		t.Errorf("claimless identity key = %q, want empty", got)
	}
}

func TestSession_GetByIdentity(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := NewSessionStore(time.Hour, 0, WithSessionClock(func() time.Time { return now }))
	sess, err := store.Create(Identity{Email: "op@example.com"}, time.Time{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok := store.GetByIdentity(Identity{Email: "op@example.com"})
	if !ok {
		t.Fatal("GetByIdentity should find the session for the same email")
	}
	if got.ID != sess.ID || got.AuditID != sess.AuditID || got.CSRFToken != sess.CSRFToken {
		t.Errorf("GetByIdentity returned a different session: %+v", got)
	}
	if !got.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Errorf("reused session expiry = %v, want the existing %v", got.ExpiresAt, sess.ExpiresAt)
	}
	// A service-token identity keys on common_name.
	if _, ok := store.GetByIdentity(Identity{CommonName: "svc"}); ok {
		t.Error("unknown common_name must not match")
	}
	if _, ok := store.GetByIdentity(Identity{Email: "other@example.com"}); ok {
		t.Error("a different email must not match")
	}
	// Lookup is idempotent and does not create anything.
	if store.Len() != 1 {
		t.Errorf("Len = %d, want 1 (lookup must not create)", store.Len())
	}
}

func TestSession_GetByIdentityNeverMatchesQuickOrClaimless(t *testing.T) {
	t.Parallel()
	store := NewSessionStore(time.Hour, 0)
	if _, err := store.Create(Identity{Quick: true}, time.Time{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Create(Identity{}, time.Time{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Quick sessions are pairing-bound: each pairing keeps its own session, so no
	// lookup may ever collapse two of them.
	if _, ok := store.GetByIdentity(Identity{Quick: true}); ok {
		t.Error("a quick identity must never be reused")
	}
	if _, ok := store.GetByIdentity(Identity{}); ok {
		t.Error("a claimless identity must never be reused")
	}
}

func TestSession_GetByIdentitySkipsExpiredAndPrefersNewest(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := NewSessionStore(time.Hour, 0, WithSessionClock(func() time.Time { return now }))
	id := Identity{Email: "op@example.com"}

	stale, _ := store.Create(id, now.Add(time.Minute)) // hard expiry in one minute
	now = now.Add(30 * time.Second)
	fresh, _ := store.Create(id, time.Time{})

	// Both live: the most recently created one wins, deterministically.
	for range 3 {
		got, ok := store.GetByIdentity(id)
		if !ok {
			t.Fatal("GetByIdentity should find a live session")
		}
		if got.ID != fresh.ID {
			t.Fatalf("GetByIdentity = %q, want the newest session %q", got.ID, fresh.ID)
		}
	}

	// Past the stale session's hard expiry it is neither returned nor retained.
	now = now.Add(2 * time.Minute)
	got, ok := store.GetByIdentity(id)
	if !ok || got.ID != fresh.ID {
		t.Fatalf("GetByIdentity after expiry = %+v, %v", got, ok)
	}
	if _, ok := store.Get(stale.ID); ok {
		t.Error("the expired session must not resolve")
	}
	if store.Len() != 1 {
		t.Errorf("Len = %d, want 1 (expired sessions evicted during lookup)", store.Len())
	}

	// Once everything expires, nothing matches.
	now = now.Add(2 * time.Hour)
	if _, ok := store.GetByIdentity(id); ok {
		t.Error("no live session must match after the TTL")
	}
}

func TestSession_GetByIdentityRefreshesLastSeen(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := NewSessionStore(12*time.Hour, 30*time.Minute, WithSessionClock(func() time.Time { return now }))
	id := Identity{Email: "op@example.com"}
	sess, _ := store.Create(id, time.Time{})

	// Repeated cookie-less arrivals inside the idle window keep the session alive
	// the same way Get does, so auto-provisioning never idles out mid-use.
	for range 4 {
		now = now.Add(20 * time.Minute)
		got, ok := store.GetByIdentity(id)
		if !ok {
			t.Fatal("session inside the idle window should be live")
		}
		if !got.LastSeen.Equal(now) {
			t.Errorf("LastSeen = %v, want %v", got.LastSeen, now)
		}
	}
	if _, ok := store.Get(sess.ID); !ok {
		t.Error("the session should still resolve by id")
	}

	now = now.Add(31 * time.Minute)
	if _, ok := store.GetByIdentity(id); ok {
		t.Error("session past the idle window must expire")
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
