package auth

import (
	"crypto/subtle"
	"net/http"
)

// CSRFHeader is the request header carrying the per-session CSRF token. It is a
// custom header, so browsers subject it to the same-origin policy: a cross-site
// page cannot set it on a request to this origin.
const CSRFHeader = "X-CSRF-Token"

// ValidCSRF reports whether provided matches expected using a constant-time
// comparison. Empty values never match.
func ValidCSRF(expected, provided string) bool {
	if expected == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

// ValidateRequestCSRF validates the CSRF token on r against the session's token.
// It reads the token from the X-CSRF-Token header. Lookup of the session is
// delegated to the store; a missing or expired session fails closed.
func (s *SessionStore) ValidateRequestCSRF(r *http.Request, sessionID string) bool {
	sess, ok := s.Get(sessionID)
	if !ok {
		return false
	}
	return ValidCSRF(sess.CSRFToken, r.Header.Get(CSRFHeader))
}

// SessionIDFromRequest extracts the opaque session identifier from the session
// cookie, or "" if absent.
func SessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
