// Package server implements the versioned loopback HTTP and WebSocket relay for
// herdr-phone. It is the only component a browser talks to.
//
// Everything the server depends on - authentication and sessions, Herdr state,
// Herdr mutations, daemon/tunnel status, workspace-root validation, audit, and
// the terminal controller launcher - is injected behind the interfaces defined
// here. Concrete implementations live in sibling packages (auth, state, herdr,
// daemon, tunnel, security); this package owns HTTP routing, the wire protocol,
// the security middleware, and the terminal WebSocket, and nothing else. That
// keeps the server unit-testable with fakes and free of import cycles.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/matheus3301/herdr-phone/internal/terminal"
)

// Identity is a verified request identity. In named mode it comes from a
// validated Cloudflare Access claim plus the app session; in quick mode it is
// the anonymous paired operator.
type Identity struct {
	// Subject is the verified email or common_name (named mode) or empty (quick).
	Subject string
	// Display is a human label for the audit UI ("Quick Tunnel operator" in
	// quick mode).
	Display string
	// SessionID is the non-secret, stable audit/display handle for the session.
	// It is NOT the session bearer cookie value and is safe to persist to the
	// audit log.
	SessionID string
	// Quick reports whether this is a quick-tunnel (no Access identity) session.
	Quick bool
	// CSRFToken is the per-session CSRF token. It is returned to the SPA on
	// GET /session so a same-origin reload can recover the token it holds only in
	// memory; it is never persisted or logged, and it is not a session bearer (a
	// mutating request still needs the HttpOnly cookie). May be empty when the
	// authenticator does not surface it.
	CSRFToken string
	// ExpiresAt is the session's expiry, surfaced to the client on GET /session.
	// The zero value means "not reported".
	ExpiresAt time.Time
}

// Session is the result of a successful pairing.
type Session struct {
	// Cookie is the opaque HttpOnly session cookie to set on the response.
	Cookie *http.Cookie
	// CSRFToken is returned to the SPA (kept in memory, never storage) and
	// echoed back in the X-CSRF-Token header on mutating requests.
	CSRFToken string
	// Identity is the identity bound to the new session.
	Identity Identity
	// ExpiresAt is the session expiry (earlier of configured TTL and Access JWT
	// expiry).
	ExpiresAt time.Time
}

// Authenticator owns pairing, sessions, CSRF, and Cloudflare Access validation.
// It is implemented by internal/auth.
type Authenticator interface {
	// NamedMode reports whether Cloudflare Access is enforced (named tunnel).
	NamedMode() bool
	// VerifyAccess validates the Cf-Access-Jwt-Assertion header in named mode.
	// It returns nil in quick mode. A non-nil error fails the request closed.
	VerifyAccess(r *http.Request) error
	// Pair validates a single-use pairing secret (constant-time), rotates it,
	// and creates a session. It returns ErrPairing on an invalid/used secret.
	Pair(r *http.Request, secret string) (*Session, error)
	// Session resolves an opaque session-cookie value to its identity. The
	// returned Identity must populate SessionID (the non-secret audit handle),
	// and should populate CSRFToken and ExpiresAt so an authenticated
	// same-origin reload can recover its CSRF token and expiry via GET /session
	// without re-pairing. The raw cookie value must never be placed in the
	// returned Identity's SessionID.
	Session(cookieValue string) (Identity, bool)
	// Revoke drops the session identified by the cookie value.
	Revoke(cookieValue string)
	// ValidateCSRF checks the per-session CSRF token for a mutating request.
	ValidateCSRF(cookieValue, csrfToken string) bool
	// CookieName is the session cookie name (e.g. __Host-herdr_phone).
	CookieName() string
}

// Snapshot is the versioned, normalized topology/agent state broadcast to
// clients. Data is opaque to the server; the state engine owns its schema.
type Snapshot struct {
	Version   int             `json:"version"`
	Hash      string          `json:"hash"`
	Data      json.RawMessage `json:"data"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// StateProvider is the read side of the Herdr state engine. It is implemented
// by internal/state.
type StateProvider interface {
	// Snapshot returns the current snapshot (source of truth).
	Snapshot() Snapshot
	// Subscribe registers fn to be called with each new snapshot. The returned
	// function unsubscribes. fn must not block.
	Subscribe(fn func(Snapshot)) (cancel func())
	// Generation returns the lifecycle generation of a pane and whether it
	// currently exists. Mutations and terminal input are checked against it.
	Generation(paneID string) (generation uint64, ok bool)
	// Capabilities returns the discovered capability document (agent kinds,
	// limits, etc.) as opaque JSON.
	Capabilities() json.RawMessage
	// ReadPane returns bounded pane content for a source/line count.
	ReadPane(ctx context.Context, paneID, source string, lines int) ([]byte, error)
	// Runs returns the run projection of the current snapshot: its content hash
	// plus authoritative run identity, pane generation, agent incarnation,
	// topology context, and status for every agent run. It never contains output
	// or transcript content, and it reads the same generation map Generation does,
	// so a run's reported generation and a mutation guard can never disagree. It
	// returns a zero RunProjection before the first successful poll, and must be
	// cheap enough to call per request (the run inbox is polled).
	Runs() RunProjection
}

// HerdrMutator executes a single allowlisted, typed Herdr operation. The server
// enforces the allowlist, generation, confirmation, deadline, and idempotency
// before calling Mutate. It is implemented by internal/herdr.
type HerdrMutator interface {
	// Mutate executes op with params under ctx (which carries the server
	// deadline). It returns a typed JSON result or a structured error.
	Mutate(ctx context.Context, op string, params json.RawMessage) (json.RawMessage, error)
}

// ComponentHealth is a bounded health record for one subsystem.
type ComponentHealth struct {
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
}

// DaemonStatus is the authenticated status view (section 17). It never contains
// secrets.
type DaemonStatus struct {
	Version  string          `json:"version"`
	Protocol int             `json:"protocol"`
	Mode     string          `json:"mode"`
	Ready    bool            `json:"ready"`
	Herdr    ComponentHealth `json:"herdr"`
	State    ComponentHealth `json:"state"`
	Clients  int             `json:"clients"`
}

// DaemonController exposes daemon lifecycle status to authenticated sessions. It
// is implemented by internal/daemon. Structural control (stop, rotate pairing)
// is deliberately not exposed over HTTP; it lives on the private control socket.
type DaemonController interface {
	Status() DaemonStatus
}

// TunnelInfo is the bounded, secret-free tunnel status.
type TunnelInfo struct {
	Mode      string          `json:"mode"`
	PublicURL string          `json:"public_url"`
	Health    ComponentHealth `json:"health"`
}

// TunnelStatus exposes cloudflared status. It is implemented by internal/tunnel.
type TunnelStatus interface {
	Tunnel() TunnelInfo
}

// DirectoryEntry is a single browsable subdirectory.
type DirectoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DirectoryValidator confines directory browsing to configured workspace roots
// without symlink escape. It is injected (implemented by internal/config or
// internal/security) so the server never itself decides what is browsable.
type DirectoryValidator interface {
	// Resolve validates path and returns the cleaned absolute directory. It
	// returns an error if path is missing, not a directory, or escapes the
	// configured roots.
	Resolve(path string) (resolved string, err error)
}

// AuditEntry is one structural audit record. It never contains terminal
// content, commands, JWTs, cookies, or pairing values.
type AuditEntry struct {
	Time      time.Time `json:"time"`
	Event     string    `json:"event"`
	Subject   string    `json:"subject,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Operation string    `json:"operation,omitempty"`
	Resource  string    `json:"resource,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Result    string    `json:"result,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Bytes     int       `json:"bytes,omitempty"`
}

// Auditor records sanitized audit entries. The server provides a 0600 JSONL
// implementation (see audit.go); tests inject a capturing fake.
type Auditor interface {
	Record(AuditEntry)
}

// Deps bundles the injected collaborators for a Server.
type Deps struct {
	Auth        Authenticator
	State       StateProvider
	Mutator     HerdrMutator
	Daemon      DaemonController
	Tunnel      TunnelStatus
	Directories DirectoryValidator
	Audit       Auditor

	// Assets serves the embedded SPA for non-API routes (from internal/webui).
	Assets http.Handler

	// TerminalLauncher launches Herdr terminal controllers for /terminals.
	TerminalLauncher terminal.Launcher
	// TerminalFilterFactory creates a fresh terminal output filter for every
	// terminal session. A per-session filter guarantees a fragmented escape
	// buffered by one controller can never bleed into a reconnect. Nil defaults
	// to a pass-through factory.
	TerminalFilterFactory func() terminal.Filter

	// QuickProbeToken, when non-empty, enables the Quick Tunnel public-instance
	// probe on GET /health: a request presenting this exact token in the probe
	// header (constant-time compared) receives InstanceID instead of "ok". It is
	// a random, injected secret and must never appear in a URL or log.
	QuickProbeToken string
	// InstanceID is the one-time instance identity returned to a valid probe.
	InstanceID string

	// Now is an injectable clock for deterministic tests; nil uses time.Now.
	Now func() time.Time
}
