package integration

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/matheus3301/herdr-phone/internal/auth"
	"github.com/matheus3301/herdr-phone/internal/daemon"
	"github.com/matheus3301/herdr-phone/internal/server"
)

// accessHeader is the only header the origin trusts for edge identity.
const accessHeader = "Cf-Access-Jwt-Assertion"

var (
	errNoAccessToken = errors.New("auth: missing Cf-Access-Jwt-Assertion header")
	// errNoAccessIdentity fails an auto-provision closed when a verified token
	// carries neither an email nor a common_name: an unattributable session
	// could not be audited or reused, so pairing remains the only way in.
	errNoAccessIdentity = errors.New("auth: access token carries no identity claim")
)

// authAdapter composes the auth package's pairing, session, CSRF, and Access
// JWT primitives into the single server.Authenticator the relay expects. It also
// serves as the daemon's pairing rotator (over the private control socket).
type authAdapter struct {
	named    bool
	pairing  *auth.Pairing
	sessions *auth.SessionStore
	verifier *auth.Verifier // nil in quick mode
	// baseURL returns the current public URL used to build pairing links. It is a
	// function because the quick-tunnel URL is only known after cloudflared is
	// ready.
	baseURL func() string
}

var _ server.Authenticator = (*authAdapter)(nil)
var _ daemon.PairingRotator = (*authAdapter)(nil)

func (a *authAdapter) NamedMode() bool { return a.named }

// VerifyAccess validates the Access JWT in named mode and is a no-op in quick
// mode. It is called by the middleware on every authenticated request.
func (a *authAdapter) VerifyAccess(r *http.Request) error {
	if !a.named {
		return nil
	}
	claims, err := a.verifyToken(r)
	if err != nil {
		return err
	}
	_ = claims
	return nil
}

func (a *authAdapter) verifyToken(r *http.Request) (*auth.Claims, error) {
	tok := r.Header.Get(accessHeader)
	if tok == "" {
		return nil, errNoAccessToken
	}
	return a.verifier.Verify(r.Context(), tok)
}

// Pair validates the single-use pairing secret and creates a session. In named
// mode the Access identity (already verified by the middleware) is re-read here
// to bind the session to the verified operator and cap it at the JWT expiry.
func (a *authAdapter) Pair(r *http.Request, secret string) (*server.Session, error) {
	var id auth.Identity
	var hardExpiry time.Time
	if a.named {
		claims, err := a.verifyToken(r)
		if err != nil {
			return nil, err
		}
		id = auth.Identity{Email: claims.Email, CommonName: claims.CommonName}
		if claims.ExpiresAt > 0 {
			hardExpiry = time.Unix(claims.ExpiresAt, 0)
		}
	} else {
		id = auth.Identity{Quick: true}
	}

	// Verify (and single-use rotate) the pairing secret only after the edge
	// identity is confirmed, so a rejected Access token never consumes it.
	if !a.pairing.Verify(secret) {
		return nil, server.ErrPairing
	}

	sess, err := a.sessions.Create(id, hardExpiry)
	if err != nil {
		return nil, err
	}
	return &server.Session{
		// The cookie carries the secret bearer id; everything the server records
		// or echoes uses the non-secret AuditID.
		Cookie:    auth.NewSessionCookie(sess.ID, sess.ExpiresAt),
		CSRFToken: sess.CSRFToken,
		Identity:  toServerIdentity(sess.AuditID, sess.CSRFToken, sess.ExpiresAt, id),
		ExpiresAt: sess.ExpiresAt,
	}, nil
}

// EnsureSession provisions the app session for a named-mode request that cleared
// the origin-side Access check but presented no valid session cookie. Named mode
// is Access-gated, so the verified edge identity is sufficient to hold a session
// and no pairing round-trip is required. Quick mode has no edge identity and
// returns (nil, nil) so single-use pairing stays the only way in.
//
// The Access claims are re-read here (rather than carried over from the
// middleware's VerifyAccess) so the session is bound to the exact identity that
// authorized this request and capped at that token's expiry. Any verification
// error is returned and the caller fails the request closed.
func (a *authAdapter) EnsureSession(r *http.Request) (*server.Session, error) {
	if !a.named {
		return nil, nil
	}
	claims, err := a.verifyToken(r)
	if err != nil {
		return nil, err
	}
	id := auth.Identity{Email: claims.Email, CommonName: claims.CommonName}
	if auth.IdentitySubject(id) == "" {
		return nil, errNoAccessIdentity
	}
	var hardExpiry time.Time
	if claims.ExpiresAt > 0 {
		hardExpiry = time.Unix(claims.ExpiresAt, 0)
	}

	// Reuse before create: a browser that keeps arriving without the cookie (or a
	// second tab that lost it) must not accumulate one session per request. A
	// reused session keeps its own expiry - already capped at the TTL and the
	// Access expiry when it was created - and is handed back with a fresh cookie
	// carrying exactly that expiry.
	sess, ok := a.sessions.GetByIdentity(id)
	if !ok {
		sess, err = a.sessions.Create(id, hardExpiry)
		if err != nil {
			return nil, err
		}
	}
	return &server.Session{
		// As in Pair: the cookie carries the secret bearer id, while everything
		// recorded or echoed uses the non-secret AuditID.
		Cookie:    auth.NewSessionCookie(sess.ID, sess.ExpiresAt),
		CSRFToken: sess.CSRFToken,
		Identity:  toServerIdentity(sess.AuditID, sess.CSRFToken, sess.ExpiresAt, sess.Identity),
		ExpiresAt: sess.ExpiresAt,
	}, nil
}

func (a *authAdapter) Session(cookieValue string) (server.Identity, bool) {
	sess, ok := a.sessions.Get(cookieValue)
	if !ok {
		return server.Identity{}, false
	}
	// SessionID exposed to the server is the non-secret AuditID, never the bearer
	// cookie value, so audit records and status can reference a session without
	// leaking a replayable credential. The CSRF token and expiry ride along so a
	// same-origin reload can recover them via GET /session.
	return toServerIdentity(sess.AuditID, sess.CSRFToken, sess.ExpiresAt, sess.Identity), true
}

// startJanitor runs the session store's expired-session GC bound to ctx (the
// daemon serve context), so abandoned sessions are evicted for a long-lived
// daemon and the goroutine stops when the daemon shuts down.
func (a *authAdapter) startJanitor(ctx context.Context) {
	a.sessions.StartJanitor(ctx, sessionJanitorInterval)
}

// sessionJanitorInterval is how often expired sessions are swept.
const sessionJanitorInterval = time.Minute

func (a *authAdapter) Revoke(cookieValue string) { a.sessions.Delete(cookieValue) }

func (a *authAdapter) ValidateCSRF(cookieValue, csrfToken string) bool {
	sess, ok := a.sessions.Get(cookieValue)
	if !ok {
		return false
	}
	return auth.ValidCSRF(sess.CSRFToken, csrfToken)
}

func (a *authAdapter) CookieName() string { return auth.CookieName }

// RotatePairing rotates the secret and returns the new pairing link, used by the
// setup-link control-socket command.
func (a *authAdapter) RotatePairing(_ context.Context) (daemon.PairingResult, error) {
	if err := a.pairing.Rotate(); err != nil {
		return daemon.PairingResult{}, err
	}
	return daemon.PairingResult{URL: a.pairing.Link(a.baseURL())}, nil
}

// toServerIdentity maps an auth.Identity plus its non-secret audit id, CSRF
// token, and expiry to the server's identity view. auditID must be the session's
// AuditID, never the bearer cookie id, so nothing durable can leak a replayable
// credential; the CSRF token and expiry let GET /session support a same-origin
// reload.
func toServerIdentity(auditID, csrfToken string, expiresAt time.Time, id auth.Identity) server.Identity {
	subject := ""
	if !id.Quick {
		if id.Email != "" {
			subject = id.Email
		} else {
			subject = id.CommonName
		}
	}
	return server.Identity{
		Subject:   subject,
		Display:   id.Display(),
		SessionID: auditID,
		Quick:     id.Quick,
		CSRFToken: csrfToken,
		ExpiresAt: expiresAt,
	}
}
