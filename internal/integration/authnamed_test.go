package integration

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/matheus3301/herdr-phone/internal/auth"
)

// ---- named-mode Access test rig --------------------------------------------

const (
	testTeamDomain = "team.cloudflareaccess.com"
	testAudience   = "test-audience"
)

// accessSigner mints Cloudflare-Access-shaped RS256 tokens for a fake JWKS, so a
// named-mode adapter can be exercised with real signature verification and no
// network, Cloudflare account, or live Access policy.
type accessSigner struct {
	priv *rsa.PrivateKey
	kid  string
	now  func() time.Time
}

func newAccessSigner(t *testing.T, now func() time.Time) *accessSigner {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &accessSigner{priv: priv, kid: "test-kid", now: now}
}

func (s *accessSigner) PublicKey(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if kid != s.kid {
		return nil, errors.New("unknown kid")
	}
	return &s.priv.PublicKey, nil
}

// token signs a token for the given claims, defaulting the time claims and the
// issuer/audience to values the verifier accepts.
func (s *accessSigner) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	full := map[string]any{
		"iss": auth.IssuerForTeam(testTeamDomain),
		"aud": []string{testAudience},
		"iat": s.now().Unix(),
		"nbf": s.now().Unix(),
		"exp": s.now().Add(time.Hour).Unix(),
	}
	maps.Copy(full, claims)
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": s.kid})
	cb, _ := json.Marshal(full)
	seg := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(seg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return seg + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// newNamedAuthAdapter builds a named-mode adapter whose Access verifier trusts
// signer, with the session store on the same clock.
func newNamedAuthAdapter(t *testing.T, signer *accessSigner, now func() time.Time, ttl time.Duration) *authAdapter {
	t.Helper()
	pairing, err := auth.NewPairing()
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewVerifier(
		auth.IssuerForTeam(testTeamDomain), testAudience, nil, signer, auth.WithClock(now),
	)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return &authAdapter{
		named:    true,
		pairing:  pairing,
		sessions: auth.NewSessionStore(ttl, 0, auth.WithSessionClock(now)),
		verifier: verifier,
		baseURL:  func() string { return "https://phone.example.com" },
	}
}

// accessRequest builds a request presenting tok as the Access assertion.
func accessRequest(tok string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	if tok != "" {
		r.Header.Set(accessHeader, tok)
	}
	return r
}

// ---- EnsureSession ---------------------------------------------------------

// TestEnsureSessionNamedMintsFromAccessIdentity is the v0.3.0 auth delta at the
// adapter level: a verified Access token alone yields an app session, bound to
// that identity, with the same cookie attributes and lifetime rules as pairing.
func TestEnsureSessionNamedMintsFromAccessIdentity(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	signer := newAccessSigner(t, clock)
	ad := newNamedAuthAdapter(t, signer, clock, 12*time.Hour)

	tok := signer.token(t, map[string]any{"email": "op@example.com"})
	sess, err := ad.EnsureSession(accessRequest(tok))
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if sess == nil {
		t.Fatal("named mode must provision a session")
	}
	if sess.Cookie == nil || sess.Cookie.Name != auth.CookieName || sess.Cookie.Value == "" {
		t.Fatalf("cookie = %+v", sess.Cookie)
	}
	if !sess.Cookie.HttpOnly || !sess.Cookie.Secure ||
		sess.Cookie.SameSite != http.SameSiteStrictMode ||
		sess.Cookie.Path != "/" || sess.Cookie.Domain != "" {
		t.Errorf("auto session cookie attributes wrong: %+v", sess.Cookie)
	}
	if sess.CSRFToken == "" {
		t.Error("auto session must carry a CSRF token")
	}
	if sess.Identity.Subject != "op@example.com" || sess.Identity.Quick {
		t.Errorf("identity = %+v", sess.Identity)
	}
	// The server-visible handle is the non-secret AuditID, never the bearer cookie.
	if sess.Identity.SessionID == "" || sess.Identity.SessionID == sess.Cookie.Value {
		t.Errorf("SessionID must be the non-secret audit id, got %q", sess.Identity.SessionID)
	}
	// The cookie authenticates: the session resolves and its CSRF token validates.
	ident, ok := ad.Session(sess.Cookie.Value)
	if !ok {
		t.Fatal("the auto-provisioned cookie must resolve to a session")
	}
	if ident.Subject != "op@example.com" || ident.CSRFToken != sess.CSRFToken {
		t.Errorf("resolved identity = %+v", ident)
	}
	if !ad.ValidateCSRF(sess.Cookie.Value, sess.CSRFToken) {
		t.Error("the auto session's CSRF token must validate")
	}
	if ad.ValidateCSRF(sess.Cookie.Value, "wrong") {
		t.Error("a wrong CSRF token must fail")
	}
	// A service-token login (common_name, no email) is equally provisionable.
	svcTok := signer.token(t, map[string]any{"common_name": "ci-runner"})
	svc, err := ad.EnsureSession(accessRequest(svcTok))
	if err != nil {
		t.Fatalf("EnsureSession(common_name): %v", err)
	}
	if svc.Identity.Subject != "ci-runner" {
		t.Errorf("service-token subject = %q", svc.Identity.Subject)
	}
}

// TestEnsureSessionCapsExpiryAtAccessExpiry keeps auto-provisioned sessions on the
// same lifetime rule as paired ones: the earlier of session_ttl and the JWT exp.
func TestEnsureSessionCapsExpiryAtAccessExpiry(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	signer := newAccessSigner(t, clock)
	ad := newNamedAuthAdapter(t, signer, clock, 12*time.Hour)

	// JWT expires well before the 12h TTL.
	hard := now.Add(5 * time.Minute)
	tok := signer.token(t, map[string]any{"email": "op@example.com", "exp": hard.Unix()})
	sess, err := ad.EnsureSession(accessRequest(tok))
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if !sess.ExpiresAt.Equal(hard) {
		t.Errorf("ExpiresAt = %v, want the Access expiry %v", sess.ExpiresAt, hard)
	}
	if !sess.Cookie.Expires.Equal(hard) {
		t.Errorf("cookie Expires = %v, want %v", sess.Cookie.Expires, hard)
	}
	if !sess.Identity.ExpiresAt.Equal(hard) {
		t.Errorf("identity ExpiresAt = %v, want %v", sess.Identity.ExpiresAt, hard)
	}

	// A short TTL wins when it is the earlier bound.
	short := newNamedAuthAdapter(t, signer, clock, time.Minute)
	shortSess, err := short.EnsureSession(accessRequest(tok))
	if err != nil {
		t.Fatalf("EnsureSession (short ttl): %v", err)
	}
	if !shortSess.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Errorf("ExpiresAt = %v, want the TTL bound %v", shortSess.ExpiresAt, now.Add(time.Minute))
	}
}

// TestEnsureSessionReusesLiveSessionForSameIdentity is the anti-accumulation
// guarantee: a browser that keeps arriving without the cookie converges on one
// session per identity, and distinct identities stay distinct.
func TestEnsureSessionReusesLiveSessionForSameIdentity(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	signer := newAccessSigner(t, clock)
	ad := newNamedAuthAdapter(t, signer, clock, 12*time.Hour)

	first, err := ad.EnsureSession(accessRequest(signer.token(t, map[string]any{"email": "op@example.com"})))
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	for i := range 20 {
		// A freshly minted token each time, exactly as a real browser would present.
		now = now.Add(time.Second)
		again, err := ad.EnsureSession(accessRequest(signer.token(t, map[string]any{"email": "op@example.com"})))
		if err != nil {
			t.Fatalf("EnsureSession %d: %v", i, err)
		}
		if again.Cookie.Value != first.Cookie.Value {
			t.Fatalf("call %d minted a new session %q, want the live %q", i, again.Cookie.Value, first.Cookie.Value)
		}
		if again.CSRFToken != first.CSRFToken {
			t.Fatalf("call %d rotated the CSRF token of a reused session", i)
		}
		// The reused session is handed back with a fresh cookie carrying its own
		// existing expiry.
		if !again.ExpiresAt.Equal(first.ExpiresAt) || !again.Cookie.Expires.Equal(first.ExpiresAt) {
			t.Fatalf("call %d changed the expiry: %v vs %v", i, again.ExpiresAt, first.ExpiresAt)
		}
	}
	if n := ad.sessions.Len(); n != 1 {
		t.Errorf("sessions after 21 cookie-less requests = %d, want 1", n)
	}

	// A different operator gets their own session.
	other, err := ad.EnsureSession(accessRequest(signer.token(t, map[string]any{"email": "other@example.com"})))
	if err != nil {
		t.Fatalf("EnsureSession (other identity): %v", err)
	}
	if other.Cookie.Value == first.Cookie.Value {
		t.Error("a different Access identity must not share a session")
	}
	if n := ad.sessions.Len(); n != 2 {
		t.Errorf("sessions = %d, want 2 (one per identity)", n)
	}

	// After the session expires, the next request provisions a fresh one rather
	// than reusing a dead record.
	now = now.Add(13 * time.Hour)
	revived, err := ad.EnsureSession(accessRequest(signer.token(t, map[string]any{"email": "op@example.com"})))
	if err != nil {
		t.Fatalf("EnsureSession after expiry: %v", err)
	}
	if revived.Cookie.Value == first.Cookie.Value {
		t.Error("an expired session must not be reused")
	}
	if _, ok := ad.Session(first.Cookie.Value); ok {
		t.Error("the expired session must no longer resolve")
	}
}

// TestEnsureSessionReusesPairedSession proves auto-provisioning composes with the
// still-supported /pair path in named mode instead of duplicating its session.
func TestEnsureSessionReusesPairedSession(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	signer := newAccessSigner(t, clock)
	ad := newNamedAuthAdapter(t, signer, clock, 12*time.Hour)

	tok := signer.token(t, map[string]any{"email": "op@example.com"})
	r := accessRequest(tok)
	paired, err := ad.Pair(r, ad.pairing.Token())
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	auto, err := ad.EnsureSession(accessRequest(tok))
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if auto.Cookie.Value != paired.Cookie.Value {
		t.Errorf("auto-provision minted %q instead of reusing the paired session %q",
			auto.Cookie.Value, paired.Cookie.Value)
	}
	if n := ad.sessions.Len(); n != 1 {
		t.Errorf("sessions = %d, want 1", n)
	}
}

// TestEnsureSessionFailsClosed covers every way an Access assertion can fail to
// establish an identity: nothing is minted and the error is returned so the
// middleware answers 401.
func TestEnsureSessionFailsClosed(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	signer := newAccessSigner(t, clock)
	other := newAccessSigner(t, clock)

	cases := []struct {
		name string
		tok  func(t *testing.T) string
		want error
	}{
		{"missing header", func(*testing.T) string { return "" }, errNoAccessToken},
		{"malformed", func(*testing.T) string { return "not-a-jwt" }, auth.ErrMalformedToken},
		{"expired", func(t *testing.T) string {
			return signer.token(t, map[string]any{"email": "op@example.com", "exp": now.Add(-2 * time.Hour).Unix()})
		}, auth.ErrExpired},
		{"wrong issuer", func(t *testing.T) string {
			return signer.token(t, map[string]any{"email": "op@example.com", "iss": "https://evil.example.com"})
		}, auth.ErrWrongIssuer},
		{"wrong audience", func(t *testing.T) string {
			return signer.token(t, map[string]any{"email": "op@example.com", "aud": []string{"other-aud"}})
		}, auth.ErrWrongAudience},
		// Both signers advertise the same kid, so a token from the wrong key is
		// caught by the signature check rather than by kid resolution.
		{"foreign signing key", func(t *testing.T) string {
			return other.token(t, map[string]any{"email": "op@example.com"})
		}, auth.ErrBadSignature},
		{"no identity claim", func(t *testing.T) string {
			return signer.token(t, nil)
		}, errNoAccessIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ad := newNamedAuthAdapter(t, signer, clock, 12*time.Hour)
			sess, err := ad.EnsureSession(accessRequest(tc.tok(t)))
			if err == nil {
				t.Fatalf("EnsureSession must fail closed, got session %+v", sess)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if sess != nil {
				t.Error("no session may be returned on failure")
			}
			if n := ad.sessions.Len(); n != 0 {
				t.Errorf("sessions = %d, want 0 (nothing minted)", n)
			}
		})
	}
}

// TestEnsureSessionQuickModeNeverProvisions pins the quick-mode contract: no edge
// identity, so pairing stays the only way in and EnsureSession is inert.
func TestEnsureSessionQuickModeNeverProvisions(t *testing.T) {
	t.Parallel()
	ad := newQuickAuthAdapter()

	// Even with an Access header present (a client cannot use it to escalate), and
	// on a plain request.
	for _, r := range []*http.Request{
		accessRequest(""),
		accessRequest("anything.at.all"),
	} {
		sess, err := ad.EnsureSession(r)
		if err != nil {
			t.Fatalf("quick EnsureSession err = %v, want nil", err)
		}
		if sess != nil {
			t.Fatalf("quick mode must not provision a session, got %+v", sess)
		}
	}
	if n := ad.sessions.Len(); n != 0 {
		t.Errorf("quick sessions = %d, want 0", n)
	}
	// Pairing still works, and its quick session is not reusable by identity.
	paired, err := ad.Pair(accessRequest(""), ad.pairing.Token())
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !paired.Identity.Quick {
		t.Error("quick pairing must yield a quick identity")
	}
	if _, ok := ad.sessions.GetByIdentity(auth.Identity{Quick: true}); ok {
		t.Error("quick sessions must never be reused by identity")
	}
}
