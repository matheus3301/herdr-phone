package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// CookieName is the session cookie name. The __Host- prefix requires the cookie
// to be Secure, Path=/, and have no Domain attribute, which the browser
// enforces — preventing a subdomain from setting or overriding it.
const CookieName = "__Host-herdr_phone"

const (
	sessionIDBytes = 32 // 256-bit opaque session identifier (bearer secret)
	csrfTokenBytes = 32 // 256-bit CSRF token
	auditIDBytes   = 16 // 128-bit non-secret per-session label for audit/display
)

// minJanitorInterval bounds how often the background GC janitor may run, so a
// misconfigured caller cannot spin the store in a tight loop.
const minJanitorInterval = time.Second

// ErrEntropy indicates the system entropy source failed while creating a
// session; callers must treat this as fatal and not proceed unauthenticated.
var ErrEntropy = errors.New("auth: entropy failure")

// Identity is the verified operator identity attached to a session. In named
// mode it comes from the Access JWT; in quick mode it is a fixed placeholder.
type Identity struct {
	Email      string
	CommonName string
	Quick      bool
}

// Display returns a human label for audit surfaces.
func (id Identity) Display() string {
	switch {
	case id.Quick:
		return "Quick Tunnel operator"
	case id.Email != "":
		return id.Email
	case id.CommonName != "":
		return id.CommonName
	default:
		return "unknown"
	}
}

// Session is an in-memory record of an authenticated operator session. Sessions
// are never persisted to disk.
type Session struct {
	// ID is the opaque 256-bit bearer secret. It is the cookie value and must
	// never be written to logs, audit records, or any other durable sink.
	ID string
	// AuditID is a distinct, non-secret 128-bit label safe to record in audit
	// logs and show on status surfaces. It cannot be replayed as a credential:
	// it is never accepted as a cookie or session identifier by the store.
	AuditID   string
	CSRFToken string
	Identity  Identity
	CreatedAt time.Time
	ExpiresAt time.Time
	LastSeen  time.Time
}

// SessionStore holds active sessions in memory, enforcing an absolute TTL and
// an optional idle timeout.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
	idle     time.Duration
	now      func() time.Time
	rand     io.Reader
}

// SessionOption customizes a SessionStore.
type SessionOption func(*SessionStore)

// WithSessionClock overrides the time source (for tests).
func WithSessionClock(now func() time.Time) SessionOption {
	return func(s *SessionStore) {
		if now != nil {
			s.now = now
		}
	}
}

// WithSessionRand overrides the entropy source (for tests).
func WithSessionRand(r io.Reader) SessionOption {
	return func(s *SessionStore) {
		if r != nil {
			s.rand = r
		}
	}
}

// NewSessionStore creates a store. ttl is the absolute session lifetime and
// idle is the inactivity timeout; an idle of zero disables idle expiry.
func NewSessionStore(ttl, idle time.Duration, opts ...SessionOption) *SessionStore {
	s := &SessionStore{
		sessions: map[string]*Session{},
		ttl:      ttl,
		idle:     idle,
		now:      time.Now,
		rand:     rand.Reader,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func randomToken(r io.Reader, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", ErrEntropy
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Create issues a new session for the given identity. The session expires at
// the earlier of the configured TTL and hardExpiry (typically the Access JWT
// expiry). A zero hardExpiry means only the TTL applies.
func (s *SessionStore) Create(id Identity, hardExpiry time.Time) (*Session, error) {
	sid, err := randomToken(s.rand, sessionIDBytes)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(s.rand, csrfTokenBytes)
	if err != nil {
		return nil, err
	}
	auditID, err := randomToken(s.rand, auditIDBytes)
	if err != nil {
		return nil, err
	}

	now := s.now()
	expires := now.Add(s.ttl)
	if !hardExpiry.IsZero() && hardExpiry.Before(expires) {
		expires = hardExpiry
	}

	sess := &Session{
		ID:        sid,
		AuditID:   auditID,
		CSRFToken: csrf,
		Identity:  id,
		CreatedAt: now,
		ExpiresAt: expires,
		LastSeen:  now,
	}

	s.mu.Lock()
	s.sessions[sid] = sess
	s.mu.Unlock()

	return sess.clone(), nil
}

func (sess *Session) clone() *Session {
	cp := *sess
	return &cp
}

func (s *SessionStore) expiredLocked(sess *Session, now time.Time) bool {
	if !now.Before(sess.ExpiresAt) {
		return true
	}
	if s.idle > 0 && now.Sub(sess.LastSeen) > s.idle {
		return true
	}
	return false
}

// Get returns a copy of the live session for id, refreshing its LastSeen. It
// returns false when the session is unknown or has expired (and removes an
// expired session). Lookup is by an unguessable 256-bit identifier.
func (s *SessionStore) Get(id string) (*Session, bool) {
	if id == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	now := s.now()
	if s.expiredLocked(sess, now) {
		delete(s.sessions, id)
		return nil, false
	}
	sess.LastSeen = now
	return sess.clone(), true
}

// Delete removes a session (logout / revocation).
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// GC removes all expired sessions and returns the number removed.
func (s *SessionStore) GC() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	n := 0
	for id, sess := range s.sessions {
		if s.expiredLocked(sess, now) {
			delete(s.sessions, id)
			n++
		}
	}
	return n
}

// Len returns the number of stored sessions (including any not yet GC'd).
func (s *SessionStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// RunJanitor evicts expired sessions on a ticker until ctx is cancelled,
// blocking the caller. The interval is clamped to a minimum so a caller cannot
// spin the store. Because expiry is otherwise only enforced lazily on Get, a
// long-lived daemon must run a janitor (or call GC) so abandoned sessions do
// not accumulate in memory.
func (s *SessionStore) RunJanitor(ctx context.Context, interval time.Duration) {
	if interval < minJanitorInterval {
		interval = minJanitorInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.GC()
		}
	}
}

// StartJanitor launches RunJanitor in a background goroutine and returns
// immediately. The goroutine exits when ctx is cancelled.
func (s *SessionStore) StartJanitor(ctx context.Context, interval time.Duration) {
	go s.RunJanitor(ctx, interval)
}

// NewSessionCookie builds the session cookie with the required __Host- prefix
// attributes: HttpOnly, Secure, SameSite=Strict, Path=/, and no Domain.
func NewSessionCookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

// ClearSessionCookie builds a cookie that immediately expires the session
// cookie on the client.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}
