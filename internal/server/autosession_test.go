package server

import (
	"net/http"
	"strings"
	"testing"
)

// autoCookieFrom returns the session cookie the response set, failing the test if
// it is missing or does not carry the required __Host- attributes.
func autoCookieFrom(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name != "__Host-herdr_phone" {
			continue
		}
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
			t.Fatalf("auto session cookie attributes wrong: %+v", c)
		}
		if c.Value == "" {
			t.Fatal("auto session cookie has no value")
		}
		return c
	}
	t.Fatalf("response set no __Host- session cookie (cookies=%v)", resp.Cookies())
	return nil
}

// TestNamedModeAutoProvisionsSessionWithoutCookie is the core of the v0.3.0 auth
// delta: in named mode a request that cleared Cloudflare Access but carries no
// app session cookie is transparently given one, with no /pair round-trip, and
// the cookie it hands back authenticates the next request.
func TestNamedModeAutoProvisionsSessionWithoutCookie(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withNamed())

	resp := h.do(http.MethodGet, apiPrefix+"/session", "", withOrigin(h.origin))
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("cookie-less named GET /session = %d, want 200", resp.StatusCode)
	}
	cookie := autoCookieFrom(t, resp)
	var sr sessionResponse
	decodeBody(t, resp, &sr)
	if sr.CSRFToken == "" {
		t.Error("auto-provisioned session must surface a CSRF token on GET /session")
	}
	if sr.Identity.Mode != "named" || sr.Identity.Subject != autoSubject || sr.Identity.Quick {
		t.Errorf("identity = %+v", sr.Identity)
	}
	if sr.ExpiresUnixMs <= 0 {
		t.Error("auto-provisioned session must report an expiry")
	}

	// The follow-up request carrying that cookie authenticates against the stored
	// session; nothing new is provisioned for it.
	follow := h.do(http.MethodGet, apiPrefix+"/snapshot", "", withCookie(cookie.Value), withOrigin(h.origin))
	follow.Body.Close()
	if follow.StatusCode != http.StatusOK {
		t.Fatalf("follow-up with auto cookie = %d, want 200", follow.StatusCode)
	}
	if n := h.auth.autoSessionCount(); n != 1 {
		t.Errorf("auto sessions minted = %d, want 1", n)
	}

	// Audit records the auto-provision with the subject and the non-secret audit
	// handle only - never the bearer cookie value.
	entries := h.audit.entriesFor("session.auto")
	if len(entries) != 1 {
		t.Fatalf("session.auto audit entries = %d, want 1 (%v)", len(entries), h.audit.events())
	}
	e := entries[0]
	if e.Subject != autoSubject || e.SessionID == "" {
		t.Errorf("session.auto entry = %+v", e)
	}
	if e.SessionID == cookie.Value || strings.Contains(e.Detail, cookie.Value) || strings.Contains(e.Resource, cookie.Value) {
		t.Error("audit must never record the session bearer cookie value")
	}
}

// TestNamedModeAutoProvisionReusesSessionForSameIdentity proves auto-provisioning
// cannot be used to grow the session store unboundedly: repeated cookie-less
// requests for the same Access identity converge on one session.
func TestNamedModeAutoProvisionReusesSessionForSameIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withNamed())

	var first string
	for i := range 6 {
		resp := h.do(http.MethodGet, apiPrefix+"/capabilities", "", withOrigin(h.origin))
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("cookie-less named request %d = %d, want 200", i, resp.StatusCode)
		}
		c := autoCookieFrom(t, resp)
		resp.Body.Close()
		if i == 0 {
			first = c.Value
		} else if c.Value != first {
			t.Fatalf("request %d got a different session cookie: %q != %q", i, c.Value, first)
		}
	}
	if n := h.auth.autoSessionCount(); n != 1 {
		t.Errorf("auto sessions minted across 6 requests = %d, want 1", n)
	}
	if n := h.auth.sessionCount(); n != 1 {
		t.Errorf("live sessions = %d, want 1", n)
	}
}

// TestNamedModeInvalidAccessDoesNotProvision keeps the Access JWT the gate: an
// unverifiable token is still a 401 and mints nothing.
func TestNamedModeInvalidAccessDoesNotProvision(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withNamed())
	h.auth.accessErr = ErrPairing // any non-nil rejects (expired, bad sig, denied)

	resp := h.do(http.MethodGet, apiPrefix+"/session", "", withOrigin(h.origin))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid Access = %d, want 401", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "__Host-herdr_phone" && c.Value != "" {
			t.Error("a rejected Access token must not set a session cookie")
		}
	}
	if n := h.auth.autoSessionCount(); n != 0 {
		t.Errorf("auto sessions minted = %d, want 0", n)
	}
	if h.audit.hasEvent("session.auto") {
		t.Error("a rejected Access token must not record session.auto")
	}
}

// TestNamedModeAutoProvisionFailsClosed covers an authenticator that refuses to
// provision (e.g. a token with no identity claim, or an entropy failure): the
// request falls through to the ordinary 401, never to an invented identity.
func TestNamedModeAutoProvisionFailsClosed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withNamed())
	h.auth.ensureErr = ErrPairing

	resp := h.do(http.MethodGet, apiPrefix+"/snapshot", "", withOrigin(h.origin))
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("failing EnsureSession = %d, want 401 (body=%s)", resp.StatusCode, body)
	}
	if h.audit.hasEvent("session.auto") {
		t.Error("a failed auto-provision must not record session.auto")
	}
}

// TestQuickModeDoesNotAutoProvision pins quick mode to today's behaviour: no
// session-bearing route is reachable without the cookie, nothing is
// auto-provisioned, and /pair remains the only way in.
func TestQuickModeDoesNotAutoProvision(t *testing.T) {
	t.Parallel()
	h := newHarness(t) // quick mode

	for _, rt := range h.server.Routes() {
		if !rt.RequiresLogin {
			continue
		}
		resp := h.do(rt.Method, sampledPath(rt.Pattern), "", withOrigin(h.origin))
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("quick %s %s without session = %d, want 401", rt.Method, rt.Pattern, resp.StatusCode)
		}
		for _, c := range resp.Cookies() {
			if c.Name == "__Host-herdr_phone" && c.Value != "" {
				t.Errorf("quick %s %s must not set a session cookie", rt.Method, rt.Pattern)
			}
		}
	}
	if n := h.auth.autoSessionCount(); n != 0 {
		t.Errorf("quick mode minted %d auto sessions, want 0", n)
	}
	if h.audit.hasEvent("session.auto") {
		t.Error("quick mode must never record session.auto")
	}

	// Pairing still works and is still the way a quick-mode client gets in.
	pair := h.do(http.MethodPost, apiPrefix+"/pair", `{"secret":"correct-secret"}`,
		withOrigin(h.origin), withHeader("Content-Type", "application/json"))
	if pair.StatusCode != http.StatusOK {
		pair.Body.Close()
		t.Fatalf("quick /pair = %d, want 200", pair.StatusCode)
	}
	paired := autoCookieFrom(t, pair)
	var pr pairResponse
	decodeBody(t, pair, &pr)
	if pr.CSRFToken == "" || pr.Identity.Mode != "quick" {
		t.Errorf("pair response = %+v", pr)
	}
	snap := h.do(http.MethodGet, apiPrefix+"/snapshot", "", withCookie(paired.Value), withOrigin(h.origin))
	snap.Body.Close()
	if snap.StatusCode != http.StatusOK {
		t.Fatalf("paired quick request = %d, want 200", snap.StatusCode)
	}
}

// TestAutoProvisionedSessionStillEnforcesOriginAndCSRF proves the rest of the
// middleware is untouched: an auto-provisioned session is not a bypass. A
// mutating request needs the Origin allowlist and the per-session CSRF token
// exactly as a paired one does.
func TestAutoProvisionedSessionStillEnforcesOriginAndCSRF(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withNamed())
	const body = `{"request_id":"r1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`

	// No Origin: the session is resolved at step 3 exactly as for a paired
	// request, and the Origin allowlist at step 4 still rejects it.
	noOrigin := h.do(http.MethodPost, apiPrefix+"/mutations", body, withHeader("Content-Type", "application/json"))
	noOrigin.Body.Close()
	if noOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie-less mutation without Origin = %d, want 403", noOrigin.StatusCode)
	}

	// Origin but no CSRF token: the session is provisioned, yet the mutation is
	// still refused until the SPA learns the token from GET /session.
	noCSRF := h.do(http.MethodPost, apiPrefix+"/mutations", body,
		withOrigin(h.origin), withHeader("Content-Type", "application/json"))
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("auto-provisioned mutation without CSRF = %d, want 403", noCSRF.StatusCode)
	}
	if h.mutator.callCount() != 0 {
		t.Fatal("a CSRF-less mutation must never reach the mutator")
	}

	// The SPA's normal path: GET /session recovers the cookie and CSRF token, then
	// the mutation succeeds.
	sess := h.do(http.MethodGet, apiPrefix+"/session", "", withOrigin(h.origin))
	cookie := autoCookieFrom(t, sess)
	var sr sessionResponse
	decodeBody(t, sess, &sr)

	ok := h.do(http.MethodPost, apiPrefix+"/mutations", body,
		withCookie(cookie.Value), withCSRF(sr.CSRFToken), withOrigin(h.origin),
		withHeader("Content-Type", "application/json"))
	var mr mutationResponse
	decodeBody(t, ok, &mr)
	if !mr.Accepted {
		t.Fatalf("mutation with the recovered CSRF token not accepted: %+v", mr)
	}
	if h.mutator.lastOp() != "pane.focus" {
		t.Fatalf("mutator op = %q", h.mutator.lastOp())
	}
}
