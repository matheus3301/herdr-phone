package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

// authLevel classifies how a route authenticates.
type authLevel int

const (
	authNone    authLevel = iota // only /health
	authPair                     // Access + Origin, but no app session yet (/pair)
	authSession                  // full: Access + session (+ CSRF if mutating)
)

// routeSpec declares one route and the security posture the central middleware
// enforces for it. Keeping every route in this table lets a single test assert
// route-wide middleware coverage (section 18).
type routeSpec struct {
	method    string
	pattern   string
	auth      authLevel
	mutating  bool
	websocket bool
	handler   http.HandlerFunc
}

// apiPrefix is the version prefix for all application routes.
const apiPrefix = "/api/v1"

func (s *Server) registerRoutes() {
	s.routes = []routeSpec{
		{method: http.MethodGet, pattern: "/health", auth: authNone, handler: s.handleHealth},

		{method: http.MethodPost, pattern: apiPrefix + "/pair", auth: authPair, mutating: true, handler: s.handlePair},
		{method: http.MethodGet, pattern: apiPrefix + "/session", auth: authSession, handler: s.handleGetSession},
		{method: http.MethodDelete, pattern: apiPrefix + "/session", auth: authSession, mutating: true, handler: s.handleDeleteSession},

		{method: http.MethodGet, pattern: apiPrefix + "/snapshot", auth: authSession, handler: s.handleSnapshot},
		{method: http.MethodGet, pattern: apiPrefix + "/panes/{pane_id}/read", auth: authSession, handler: s.handlePaneRead},
		{method: http.MethodGet, pattern: apiPrefix + "/directories", auth: authSession, handler: s.handleDirectories},
		{method: http.MethodGet, pattern: apiPrefix + "/capabilities", auth: authSession, handler: s.handleCapabilities},

		{method: http.MethodGet, pattern: apiPrefix + "/events", auth: authSession, websocket: true, handler: s.handleEvents},
		{method: http.MethodGet, pattern: apiPrefix + "/terminals/{pane_id}", auth: authSession, websocket: true, handler: s.handleTerminal},

		{method: http.MethodPost, pattern: apiPrefix + "/confirmations", auth: authSession, mutating: true, handler: s.handleConfirmations},
		{method: http.MethodPost, pattern: apiPrefix + "/mutations", auth: authSession, mutating: true, handler: s.handleMutations},
	}
	for _, rt := range s.routes {
		s.mux.Handle(rt.method+" "+rt.pattern, s.wrap(rt))
	}
	// Everything else is the SPA shell (client-side routes, static assets).
	s.mux.Handle("/", s.spaHandler())
}

// Routes returns a copy of the route table for coverage tests.
func (s *Server) Routes() []RouteInfo {
	out := make([]RouteInfo, 0, len(s.routes))
	for _, rt := range s.routes {
		out = append(out, RouteInfo{
			Method:        rt.method,
			Pattern:       rt.pattern,
			RequiresAuth:  rt.auth != authNone,
			RequiresLogin: rt.auth == authSession,
			Mutating:      rt.mutating,
			WebSocket:     rt.websocket,
		})
	}
	return out
}

// RouteInfo is the exported view of a route's security posture.
type RouteInfo struct {
	Method        string
	Pattern       string
	RequiresAuth  bool
	RequiresLogin bool
	Mutating      bool
	WebSocket     bool
}

type ctxKey int

const identityKey ctxKey = iota

func identityFrom(ctx context.Context) Identity {
	id, _ := ctx.Value(identityKey).(Identity)
	return id
}

// wrap applies the central security middleware (section 9.3) to a route in the
// exact required order.
func (s *Server) wrap(rt routeSpec) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.securityHeaders(w, true)

		// 1. Host allowlist.
		if !s.hostAllowed(r.Host) {
			writeError(w, http.StatusMisdirectedRequest, codeForbidden, "host not allowed")
			return
		}

		// 2. Access JWT (named mode) for any authenticated route.
		if rt.auth != authNone {
			if err := s.deps.Auth.VerifyAccess(r); err != nil {
				writeError(w, http.StatusUnauthorized, codeUnauthorized, "access denied")
				return
			}
		}

		// 3. App session (except /pair). CSRF is validated at step 5.
		var ident Identity
		var cookieVal string
		// Unauthenticated routes get a per-route bucket keyed by client + route so
		// a flood on one (e.g. public /health) cannot starve another (e.g. /pair);
		// behind cloudflared every client IP is loopback, so without the route the
		// buckets would collapse into one. Authenticated routes key per session.
		rateKey := "u:" + rt.pattern + "|" + clientIP(r)
		if rt.auth == authSession {
			cv, id, ok := s.sessionFromRequest(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, codeUnauthorized, "no valid session")
				return
			}
			ident = id
			cookieVal = cv
			rateKey = "s:" + id.SessionID
		}

		// 4. Exact Origin allowlist for WebSocket and mutating requests.
		if rt.websocket || rt.mutating {
			if !s.originAllowed(r) {
				writeError(w, http.StatusForbidden, codeForbidden, "origin not allowed")
				return
			}
			// 5a. Go CrossOriginProtection as defense in depth.
			if err := s.cop.Check(r); err != nil {
				writeError(w, http.StatusForbidden, codeForbidden, "cross-origin request blocked")
				return
			}
		}

		// 5b. CSRF custom-header/token for mutating session requests.
		if rt.auth == authSession && rt.mutating {
			if !s.deps.Auth.ValidateCSRF(cookieVal, r.Header.Get("X-CSRF-Token")) {
				writeError(w, http.StatusForbidden, codeForbidden, "invalid CSRF token")
				return
			}
		}

		// 6a. Content-type allowlist for JSON POST bodies.
		if rt.mutating && rt.method == http.MethodPost {
			if !hasJSONContentType(r) {
				writeError(w, http.StatusUnsupportedMediaType, codeUnsupportedMedia, "expected application/json")
				return
			}
		}

		// 6b. Bounded bodies for non-WebSocket requests.
		if !rt.websocket && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
		}

		// 6c. Rate limit (WebSocket handshakes count too).
		if !s.rl.allow(rateKey) {
			writeError(w, http.StatusTooManyRequests, codeRateLimited, "rate limit exceeded")
			return
		}

		// 6d. Deadline for normal requests; never for long-lived WebSockets.
		ctx := context.WithValue(r.Context(), identityKey, ident)
		if !rt.websocket && s.cfg.RequestTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.cfg.RequestTimeout)
			defer cancel()
		}
		rt.handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sessionFromRequest reads and validates the app session cookie.
func (s *Server) sessionFromRequest(r *http.Request) (cookieValue string, ident Identity, ok bool) {
	c, err := r.Cookie(s.deps.Auth.CookieName())
	if err != nil || c.Value == "" {
		return "", Identity{}, false
	}
	id, ok := s.deps.Auth.Session(c.Value)
	if !ok {
		return "", Identity{}, false
	}
	return c.Value, id, true
}

// spaHandler serves the embedded application shell for all non-API routes.
func (s *Server) spaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "host not allowed", http.StatusMisdirectedRequest)
			return
		}
		// API paths that fall through to here are unknown endpoints.
		if strings.HasPrefix(r.URL.Path, apiPrefix+"/") {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown endpoint")
			return
		}
		s.securityHeaders(w, false)
		if s.deps.Assets == nil {
			http.Error(w, "application shell unavailable", http.StatusServiceUnavailable)
			return
		}
		s.deps.Assets.ServeHTTP(w, r)
	})
}

// ProbeHeader carries the Quick Tunnel public-instance probe token. It is a
// dedicated header (never a URL parameter) so the secret token cannot land in
// access logs, browser history, or referrers.
const ProbeHeader = "X-Herdr-Phone-Probe"

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Minimal liveness by default: no topology/version/instance detail
	// (section 9.3). If a probe token is configured (Quick Tunnel mode) and the
	// request presents the exact token, return the instance id so the daemon can
	// confirm the public URL reaches this same instance (section 17). The token
	// is validated in constant time and is never logged.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.probeToken != "" {
		if presented := r.Header.Get(ProbeHeader); presented != "" &&
			subtle.ConstantTimeCompare([]byte(presented), []byte(s.probeToken)) == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(s.instanceID))
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func hasJSONContentType(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	// Accept "application/json" optionally followed by parameters.
	base, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(base), "application/json")
}

// clientIP returns a rate-limit key for unauthenticated routes.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return "unknown"
	}
	return host
}
