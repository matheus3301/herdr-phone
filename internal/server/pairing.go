package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type pairRequest struct {
	Secret string `json:"secret"`
}

type pairResponse struct {
	CSRFToken     string `json:"csrf_token"`
	ExpiresUnixMs int64  `json:"expires_unix_ms"`
	Identity      idJSON `json:"identity"`
}

type idJSON struct {
	Subject string `json:"subject"`
	Display string `json:"display"`
	Quick   bool   `json:"quick"`
	Mode    string `json:"mode"`
}

func (s *Server) mode() string {
	if s.deps.Auth.NamedMode() {
		return "named"
	}
	return "quick"
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var req pairRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid request body")
		return
	}
	if req.Secret == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "missing pairing secret")
		return
	}

	sess, err := s.deps.Auth.Pair(r, req.Secret)
	if err != nil {
		if errors.Is(err, ErrPairing) {
			s.deps.Audit.Record(AuditEntry{Event: "pair.reject", Result: "invalid"})
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "pairing rejected")
			return
		}
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "pairing unavailable")
		return
	}

	http.SetCookie(w, sess.Cookie)
	s.deps.Audit.Record(AuditEntry{
		Event:     "pair.success",
		Subject:   sess.Identity.Subject,
		SessionID: sess.Identity.SessionID,
	})
	writeJSON(w, http.StatusOK, pairResponse{
		CSRFToken:     sess.CSRFToken,
		ExpiresUnixMs: sess.ExpiresAt.UnixMilli(),
		Identity: idJSON{
			Subject: sess.Identity.Subject,
			Display: sess.Identity.Display,
			Quick:   sess.Identity.Quick,
			Mode:    s.mode(),
		},
	})
}

// sessionResponse lets an authenticated same-origin reload recover its session's
// identity, expiry, and the per-session CSRF token it holds only in memory. It
// mirrors the pair response shape but is a distinct type so the pair response is
// untouched. It never carries the session bearer cookie value.
type sessionResponse struct {
	CSRFToken     string `json:"csrf_token"`
	ExpiresUnixMs int64  `json:"expires_unix_ms"`
	Identity      idJSON `json:"identity"`
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	ident := identityFrom(r.Context())
	var expires int64
	if !ident.ExpiresAt.IsZero() {
		expires = ident.ExpiresAt.UnixMilli()
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		CSRFToken:     ident.CSRFToken,
		ExpiresUnixMs: expires,
		Identity: idJSON{
			Subject: ident.Subject,
			Display: ident.Display,
			Quick:   ident.Quick,
			Mode:    s.mode(),
		},
	})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.deps.Auth.CookieName()); err == nil && c.Value != "" {
		s.deps.Auth.Revoke(c.Value)
	}
	ident := identityFrom(r.Context())
	// Clear the cookie with the same attributes required for __Host- deletion.
	http.SetCookie(w, &http.Cookie{
		Name:     s.deps.Auth.CookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	s.deps.Audit.Record(AuditEntry{Event: "session.revoke", Subject: ident.Subject, SessionID: ident.SessionID})
	w.WriteHeader(http.StatusNoContent)
}

// decodeJSON strictly decodes a request body, rejecting unknown fields and
// trailing data.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Reject any trailing content after the first JSON value.
	if dec.More() {
		return errors.New("unexpected trailing data")
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return err
	}
	return nil
}
