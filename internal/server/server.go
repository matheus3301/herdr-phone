package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/matheus3301/herdr-phone/internal/interpret"
	"github.com/matheus3301/herdr-phone/internal/terminal"
)

// Config holds the server's non-injected settings, derived from the daemon
// config (section 8).
type Config struct {
	// PublicHost is the exact Host header expected from the public front door
	// (e.g. herdr.example.com). Required.
	PublicHost string
	// DevHosts are additional exact loopback hosts allowed in development
	// (e.g. 127.0.0.1:8787).
	DevHosts []string
	// AllowedOrigins is the exact Origin allowlist for WebSocket handshakes and
	// mutating requests (scheme+host, no trailing slash).
	AllowedOrigins []string
	// Quick reports quick-tunnel mode (no Access identity; pairing still
	// mandatory).
	Quick bool

	MaxBodyBytes           int64
	RequestTimeout         time.Duration
	ServerMutationDeadline time.Duration
	DeadlineSkew           time.Duration
	IdempotencyTTL         time.Duration
	ConfirmationTTL        time.Duration
	RateEvery              time.Duration
	RateBurst              int
	MaxPaneReadLines       int

	// Run contract bounds (SPEC §12.1). Observed run output is bounded tighter
	// than a raw pane read: it is a supervision view, not a log export.
	MaxRunOutputLines int
	MaxRunOutputBytes int
	MaxRuns           int

	// Interpretation is the experimental heuristic-parsing configuration
	// (SPEC §12.2). The zero value is off, so a Server built without setting it
	// behaves exactly as a build without the feature.
	Interpretation Interpretation

	// Terminal tunables passed through to each terminal bridge.
	Terminal terminal.Options
}

// Interpretation configures experimental heuristic interpretation of agent
// terminal text. It is a narrow projection of config.Experimental rather than the
// whole thing, so the server cannot come to depend on unrelated config.
type Interpretation struct {
	// Enabled gates every part of the feature. While false, no parser runs and no
	// capability or part type is advertised.
	Enabled bool
	// Parsers is the set of agent kinds whose grammar may be parsed.
	Parsers []string
	// MaxTurns bounds the published transcript.
	MaxTurns int
}

// parses reports whether interpretation is enabled for one agent kind. A pane
// running any other agent is never parsed.
func (i Interpretation) parses(agentKind string) bool {
	if !i.Enabled || agentKind == "" {
		return false
	}
	return slices.Contains(i.Parsers, agentKind)
}

func (c *Config) applyDefaults() {
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 1 << 20 // 1 MiB
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 15 * time.Second
	}
	if c.ServerMutationDeadline <= 0 {
		c.ServerMutationDeadline = 10 * time.Second
	}
	if c.DeadlineSkew <= 0 {
		c.DeadlineSkew = 500 * time.Millisecond
	}
	if c.IdempotencyTTL <= 0 {
		c.IdempotencyTTL = 5 * time.Minute
	}
	if c.ConfirmationTTL <= 0 {
		c.ConfirmationTTL = 30 * time.Second
	}
	if c.RateEvery <= 0 {
		c.RateEvery = 100 * time.Millisecond
	}
	if c.RateBurst <= 0 {
		c.RateBurst = 40
	}
	if c.MaxPaneReadLines <= 0 {
		c.MaxPaneReadLines = 2000
	}
	if c.MaxRunOutputLines <= 0 {
		c.MaxRunOutputLines = 400
	}
	if c.MaxRunOutputBytes <= 0 {
		c.MaxRunOutputBytes = 64 << 10 // 64 KiB
	}
	if c.MaxRuns <= 0 {
		c.MaxRuns = 200
	}
	if c.Interpretation.MaxTurns <= 0 {
		c.Interpretation.MaxTurns = interpret.DefaultLimits().MaxTurns
	}
}

// Server is the loopback HTTP/WebSocket relay.
type Server struct {
	cfg  Config
	deps Deps
	mux  *http.ServeMux
	cop  *http.CrossOriginProtection
	now  func() time.Time

	hub    *hub
	nonces *nonceStore
	idem   *idemStore
	rl     *rateLimiter

	baseCtx    context.Context
	baseCancel context.CancelFunc

	probeToken string
	instanceID string

	csp string // precomputed Content-Security-Policy header value

	routes []routeSpec
}

// New validates dependencies and builds a ready Server.
func New(cfg Config, deps Deps) (*Server, error) {
	cfg.applyDefaults()
	if deps.Auth == nil {
		return nil, errors.New("server: Auth dependency is required")
	}
	if deps.State == nil {
		return nil, errors.New("server: State dependency is required")
	}
	if deps.Mutator == nil {
		return nil, errors.New("server: Mutator dependency is required")
	}
	if deps.Daemon == nil {
		return nil, errors.New("server: Daemon dependency is required")
	}
	if deps.Tunnel == nil {
		return nil, errors.New("server: Tunnel dependency is required")
	}
	if deps.Directories == nil {
		return nil, errors.New("server: Directories dependency is required")
	}
	if cfg.PublicHost == "" && len(cfg.DevHosts) == 0 {
		return nil, errors.New("server: no allowed hosts configured")
	}
	if deps.Audit == nil {
		deps.Audit = nopAuditor{}
	}
	// Terminal filtering fails closed: a nil factory is left nil so handleTerminal
	// refuses to open an unfiltered terminal rather than relaying raw escapes.
	now := deps.Now
	if now == nil {
		now = time.Now
	}

	cop := http.NewCrossOriginProtection()
	for _, o := range cfg.AllowedOrigins {
		// AddTrustedOrigin validates the origin form; ignore individual errors so
		// one malformed configured origin does not break startup, it just is not
		// trusted.
		_ = cop.AddTrustedOrigin(o)
	}

	baseCtx, baseCancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:        cfg,
		deps:       deps,
		mux:        http.NewServeMux(),
		cop:        cop,
		now:        now,
		nonces:     newNonceStore(now),
		idem:       newIdemStore(now),
		rl:         newRateLimiter(cfg.RateEvery, cfg.RateBurst, now),
		baseCtx:    baseCtx,
		baseCancel: baseCancel,
		probeToken: deps.QuickProbeToken,
		instanceID: deps.InstanceID,
		csp:        buildCSP(cfg.AllowedOrigins),
	}
	s.hub = newHub(deps.State)
	s.registerRoutes()
	return s, nil
}

// Handler returns the http.Handler for the server.
func (s *Server) Handler() http.Handler { return s.mux }

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Close releases background resources: it cancels in-flight terminal sessions
// and drops the state subscription.
func (s *Server) Close() {
	s.baseCancel()
	s.hub.close()
}

// Clients returns the number of connected event clients (for status).
func (s *Server) Clients() int { return s.hub.count() }

// hostAllowed enforces the Host allowlist (exact match).
func (s *Server) hostAllowed(host string) bool {
	if host == "" {
		return false
	}
	if s.cfg.PublicHost != "" && host == s.cfg.PublicHost {
		return true
	}
	return slices.Contains(s.cfg.DevHosts, host)
}

// originAllowed enforces the exact Origin allowlist. A missing Origin on a
// same-origin non-browser request is not accepted for mutating/WS routes: those
// require a browser Origin we can match.
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, o := range s.cfg.AllowedOrigins {
		if strings.EqualFold(origin, o) {
			return true
		}
	}
	return false
}

// securityHeaders sets the strict response headers required by section 9.3.
func (s *Server) securityHeaders(w http.ResponseWriter, api bool) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), usb=()")
	// CSP: self-hosted only, no objects/base/framing, WebSocket scoped to the
	// exact configured origins, no unsafe-eval, no CDN.
	h.Set("Content-Security-Policy", s.csp)
	if api {
		h.Set("Cache-Control", "no-store")
	}
}

// buildCSP precomputes the Content-Security-Policy. connect-src is scoped to the
// exact configured origins' WebSocket equivalents (https→wss, http→ws) plus
// 'self', instead of the scheme-wide ws:/wss: allowance, so a script cannot open
// a socket to an arbitrary host (SPEC §9.3).
func buildCSP(allowedOrigins []string) string {
	connect := []string{"'self'"}
	for _, o := range allowedOrigins {
		switch {
		case strings.HasPrefix(o, "https://"):
			connect = append(connect, "wss://"+strings.TrimPrefix(o, "https://"))
		case strings.HasPrefix(o, "http://"):
			connect = append(connect, "ws://"+strings.TrimPrefix(o, "http://"))
		}
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self'",
		"connect-src " + strings.Join(connect, " "),
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	}, "; ")
}
