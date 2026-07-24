package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func makeDir(path string) error  { return os.Mkdir(path, 0o755) }
func makeFile(path string) error { return os.WriteFile(path, []byte("x"), 0o644) }
func itoa(n int64) string        { return strconv.FormatInt(n, 10) }

// ---- middleware / route coverage ------------------------------------------

func TestHealthIsUnauthenticatedAndMinimal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.do(http.MethodGet, "/health", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if got := strings.TrimSpace(string(buf[:n])); got != "ok" {
		t.Fatalf("health body = %q, want ok", got)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func TestHealthProbeReturnsInstanceIDOnValidToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withProbe("secret-probe-token", "instance-abc"))

	// No probe header: bare ok.
	if got := readBody(t, h.do(http.MethodGet, "/health", "")); got != "ok" {
		t.Fatalf("plain health = %q, want ok", got)
	}
	// Wrong token: bare ok (never reveals instance id).
	if got := readBody(t, h.do(http.MethodGet, "/health", "", withHeader(ProbeHeader, "wrong"))); got != "ok" {
		t.Fatalf("bad-token health = %q, want ok", got)
	}
	// Correct token: instance id.
	if got := readBody(t, h.do(http.MethodGet, "/health", "", withHeader(ProbeHeader, "secret-probe-token"))); got != "instance-abc" {
		t.Fatalf("probed health = %q, want instance-abc", got)
	}
}

func TestHealthProbeDisabledByDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t) // no probe token configured
	// Even with a probe header, a server without a configured token returns ok.
	got := readBody(t, h.do(http.MethodGet, "/health", "", withHeader(ProbeHeader, "anything")))
	if got != "ok" {
		t.Fatalf("health = %q, want ok when probe disabled", got)
	}
}

func TestEveryAPIRouteRequiresSessionExceptPair(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, rt := range h.server.Routes() {
		if !rt.RequiresLogin {
			continue // /health and /pair are exercised separately
		}
		// No cookie: must be rejected before reaching the handler.
		resp := h.do(rt.Method, sampledPath(rt.Pattern), "", withOrigin(h.origin))
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without session = %d, want 401", rt.Method, rt.Pattern, resp.StatusCode)
		}
	}
}

func TestRouteTableCoversSpecEndpoints(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	want := []string{
		"/health",
		apiPrefix + "/pair",
		apiPrefix + "/session",
		apiPrefix + "/snapshot",
		apiPrefix + "/panes/{pane_id}/read",
		apiPrefix + "/directories",
		apiPrefix + "/capabilities",
		apiPrefix + "/events",
		apiPrefix + "/terminals/{pane_id}",
		apiPrefix + "/confirmations",
		apiPrefix + "/mutations",
	}
	have := map[string]bool{}
	for _, rt := range h.server.Routes() {
		have[rt.Pattern] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("route table missing %s", w)
		}
	}
}

// sampledPath substitutes a concrete id for a wildcard pattern.
func sampledPath(pattern string) string {
	return strings.ReplaceAll(pattern, "{pane_id}", "pane-1")
}

func TestWrongHostRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, _ := h.sessionCookie()
	resp := h.do(http.MethodGet, apiPrefix+"/snapshot", "",
		withCookie(cookie), withOrigin(h.origin), withHost("evil.example.com"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("status = %d, want 421", resp.StatusCode)
	}
}

func TestMutatingRequiresOrigin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, csrf := h.sessionCookie()
	// No Origin header on a mutating request.
	resp := h.do(http.MethodPost, apiPrefix+"/mutations", `{"request_id":"r","operation":"pane.focus"}`,
		withCookie(cookie), withCSRF(csrf), withHeader("Content-Type", "application/json"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestMutatingRequiresCSRF(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, _ := h.sessionCookie()
	resp := h.do(http.MethodPost, apiPrefix+"/mutations", `{"request_id":"r","operation":"pane.focus"}`,
		withCookie(cookie), withOrigin(h.origin), withHeader("Content-Type", "application/json"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing CSRF)", resp.StatusCode)
	}
}

func TestMutatingRequiresJSONContentType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, csrf := h.sessionCookie()
	resp := h.do(http.MethodPost, apiPrefix+"/mutations", `{"request_id":"r","operation":"pane.focus"}`,
		withCookie(cookie), withCSRF(csrf), withOrigin(h.origin), withHeader("Content-Type", "text/plain"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", resp.StatusCode)
	}
}

func TestSecurityHeadersOnAPI(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/capabilities")
	resp.Body.Close()
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("missing CSP")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe-eval") {
		t.Error("CSP must not allow unsafe-eval")
	}
}

func TestNamedModeAccessRequired(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withNamed())
	h.auth.accessErr = ErrPairing // any non-nil rejects
	resp := h.authedGET(apiPrefix + "/snapshot")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when Access fails", resp.StatusCode)
	}
}

// ---- pairing / session ----------------------------------------------------

func TestPairSuccessSetsCookieAndCSRF(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.do(http.MethodPost, apiPrefix+"/pair", `{"secret":"correct-secret"}`,
		withOrigin(h.origin), withHeader("Content-Type", "application/json"))
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var pr pairResponse
	decodeBody(t, resp, &pr)
	if pr.CSRFToken == "" {
		t.Error("expected CSRF token")
	}
	var sawCookie bool
	for _, c := range resp.Cookies() {
		if c.Name == "__Host-herdr_phone" && c.HttpOnly && c.Secure {
			sawCookie = true
		}
	}
	if !sawCookie {
		t.Error("expected HttpOnly Secure session cookie")
	}
	if !h.audit.hasEvent("pair.success") {
		t.Error("expected pair.success audit entry")
	}
}

func TestPairInvalidSecretRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.do(http.MethodPost, apiPrefix+"/pair", `{"secret":"wrong"}`,
		withOrigin(h.origin), withHeader("Content-Type", "application/json"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPairIsSingleUse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first := h.do(http.MethodPost, apiPrefix+"/pair", `{"secret":"correct-secret"}`,
		withOrigin(h.origin), withHeader("Content-Type", "application/json"))
	first.Body.Close()
	// The fake rotated the secret; reusing the original must now fail.
	second := h.do(http.MethodPost, apiPrefix+"/pair", `{"secret":"correct-secret"}`,
		withOrigin(h.origin), withHeader("Content-Type", "application/json"))
	second.Body.Close()
	if second.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused secret status = %d, want 401", second.StatusCode)
	}
}

func TestSessionGetAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, csrf := h.sessionCookie()

	get := h.do(http.MethodGet, apiPrefix+"/session", "", withCookie(cookie), withOrigin(h.origin))
	var sr sessionResponse
	decodeBody(t, get, &sr)
	if sr.Identity.Subject != "op@example.com" {
		t.Fatalf("session subject = %q", sr.Identity.Subject)
	}

	del := h.do(http.MethodDelete, apiPrefix+"/session", "", withCookie(cookie), withCSRF(csrf), withOrigin(h.origin))
	del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", del.StatusCode)
	}
	if _, ok := h.auth.Session(cookie); ok {
		t.Error("session should be revoked")
	}
}

func TestSessionReturnsCSRFAndExpiryWithoutCookie(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, csrf := h.sessionCookie()

	req, err := http.NewRequest(http.MethodGet, h.srv.URL+apiPrefix+"/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	withCookie(cookie)(req)
	withOrigin(h.origin)(req)
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var sr sessionResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A reload recovers its in-memory CSRF token and the session expiry.
	if sr.CSRFToken != csrf {
		t.Fatalf("csrf_token = %q, want %q", sr.CSRFToken, csrf)
	}
	if sr.ExpiresUnixMs == 0 {
		t.Error("expected non-zero expires_unix_ms")
	}
	if sr.Identity.Subject != "op@example.com" {
		t.Fatalf("identity subject = %q", sr.Identity.Subject)
	}
	// The bearer cookie value must never appear in the response.
	if strings.Contains(string(body), cookie) {
		t.Fatalf("session response leaked the cookie bearer %q: %s", cookie, body)
	}
}

// ---- snapshot / capabilities / pane read ----------------------------------

func TestSnapshotETagAnd304(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/snapshot")
	etag := resp.Header.Get("ETag")
	resp.Body.Close()
	if etag == "" {
		t.Fatal("missing ETag")
	}
	// Conditional GET with matching ETag returns 304.
	resp2 := h.authedGET(apiPrefix+"/snapshot", withHeader("If-None-Match", etag))
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", resp2.StatusCode)
	}
}

func TestSnapshotGzip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Make the snapshot large enough to trigger gzip.
	big := strings.Repeat("x", 2048)
	h.state.push(Snapshot{Version: 2, Hash: "hash-2", Data: json.RawMessage(`{"blob":"` + big + `"}`)})
	resp := h.authedGET(apiPrefix+"/snapshot", withHeader("Accept-Encoding", "gzip"))
	resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip, got %q", resp.Header.Get("Content-Encoding"))
	}
}

func TestCapabilitiesIncludesOperationsAndStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/capabilities")
	var cr capabilitiesResponse
	decodeBody(t, resp, &cr)
	if len(cr.Operations) == 0 {
		t.Fatal("expected operation allowlist")
	}
	found := false
	for _, op := range cr.Operations {
		if op == "pane.split" {
			found = true
		}
	}
	if !found {
		t.Error("operations should include pane.split")
	}
	if cr.Status.Version != "0.1.0" {
		t.Errorf("status version = %q", cr.Status.Version)
	}
	if cr.Tunnel.PublicURL == "" {
		t.Error("expected tunnel public url")
	}
}

func TestPaneReadValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Invalid source.
	bad := h.authedGET(apiPrefix + "/panes/pane-1/read?source=bogus")
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad source status = %d, want 400", bad.StatusCode)
	}
	// Valid.
	ok := h.authedGET(apiPrefix + "/panes/pane-1/read?source=visible&lines=50")
	var pr paneReadResponse
	decodeBody(t, ok, &pr)
	if pr.Content != "last visible output" {
		t.Fatalf("content = %q", pr.Content)
	}
	if pr.Lines != 50 {
		t.Fatalf("lines = %d", pr.Lines)
	}
}

func TestPaneReadLinesBounded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/panes/pane-1/read?source=recent&lines=99999")
	var pr paneReadResponse
	decodeBody(t, resp, &pr)
	if pr.Lines > 2000 {
		t.Fatalf("lines not bounded: %d", pr.Lines)
	}
}

// ---- directories ----------------------------------------------------------

func TestDirectoriesConfinedAndDirsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Create a subdir and a file in the temp root.
	root := h.dirs.root
	if err := makeDir(root + "/sub"); err != nil {
		t.Fatal(err)
	}
	if err := makeFile(root + "/file.txt"); err != nil {
		t.Fatal(err)
	}
	resp := h.authedGET(apiPrefix + "/directories?path=" + root)
	var dr directoriesResponse
	decodeBody(t, resp, &dr)
	if len(dr.Entries) != 1 || dr.Entries[0].Name != "sub" {
		t.Fatalf("entries = %+v, want only [sub]", dr.Entries)
	}

	// A path outside the root is rejected by the validator.
	out := h.authedGET(apiPrefix + "/directories?path=/etc")
	out.Body.Close()
	if out.StatusCode != http.StatusForbidden {
		t.Fatalf("outside path status = %d, want 403", out.StatusCode)
	}
}

// ---- mutations ------------------------------------------------------------

func TestMutationAllowlistRejectsUnknown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", `{"request_id":"r1","operation":"server.stop"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for disallowed op", resp.StatusCode)
	}
	if h.mutator.callCount() != 0 {
		t.Fatal("disallowed op must not reach the mutator")
	}
}

func TestMutationSuccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := `{"request_id":"r1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	if !mr.Accepted {
		t.Fatalf("expected accepted, got %+v", mr)
	}
	if h.mutator.lastOp() != "pane.focus" {
		t.Fatalf("mutator op = %q", h.mutator.lastOp())
	}
	if !h.audit.hasEvent("mutation") {
		t.Error("expected mutation audit entry")
	}
}

func TestMutationIdempotencyReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := `{"request_id":"same-id","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`
	r1 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	r1.Body.Close()
	r2 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	replay := r2.Header.Get("Idempotent-Replay")
	r2.Body.Close()
	if replay != "true" {
		t.Fatalf("expected idempotent replay header, got %q", replay)
	}
	if h.mutator.callCount() != 1 {
		t.Fatalf("mutator called %d times, want 1 (idempotent)", h.mutator.callCount())
	}
}

func TestMutationGenerationStale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// pane-1 generation is 7; pane.focus is generation-checked but needs no
	// confirmation, so this isolates the generation guard.
	body := `{"request_id":"r1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":3}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	if mr.Error == nil || mr.Error.Code != codeGenerationStale {
		t.Fatalf("expected generation_stale, got %+v", mr.Error)
	}
	if !mr.Error.Retryable {
		t.Error("generation_stale should be retryable")
	}
	if h.mutator.callCount() != 0 {
		t.Fatal("stale generation must not dispatch")
	}
}

func TestConfirmationFlowForDestructiveOp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Without confirmation, a destructive op is rejected.
	noConf := h.authedJSON(http.MethodPost, apiPrefix+"/mutations",
		`{"request_id":"rc","operation":"pane.close","params":{"pane_id":"pane-1"},"expected_generation":7}`)
	var mr1 mutationResponse
	decodeBody(t, noConf, &mr1)
	if mr1.Error == nil || mr1.Error.Code != codeConfirmationNeeded {
		t.Fatalf("expected confirmation_required, got %+v", mr1.Error)
	}

	// Obtain a confirmation nonce.
	confResp := h.authedJSON(http.MethodPost, apiPrefix+"/confirmations",
		`{"operation":"pane.close","resource_id":"pane-1","expected_generation":7,"params":{"pane_id":"pane-1"}}`)
	var cr confirmationResponse
	decodeBody(t, confResp, &cr)
	if cr.Confirmation == "" {
		t.Fatal("expected confirmation nonce")
	}

	// Use it: mutation succeeds.
	body := `{"request_id":"rc2","operation":"pane.close","params":{"pane_id":"pane-1"},"expected_generation":7,"confirmation":"` + cr.Confirmation + `"}`
	ok := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr2 mutationResponse
	decodeBody(t, ok, &mr2)
	if !mr2.Accepted {
		t.Fatalf("expected accepted after confirmation, got %+v", mr2)
	}

	// The nonce is single-use: replaying with a fresh request id fails.
	body2 := `{"request_id":"rc3","operation":"pane.close","params":{"pane_id":"pane-1"},"expected_generation":7,"confirmation":"` + cr.Confirmation + `"}`
	reuse := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body2)
	var mr3 mutationResponse
	decodeBody(t, reuse, &mr3)
	if mr3.Error == nil || mr3.Error.Code != codeConfirmationInvalid {
		t.Fatalf("expected confirmation_invalid on reuse, got %+v", mr3.Error)
	}
}

func TestConfirmationGenerationMismatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/confirmations",
		`{"operation":"pane.close","resource_id":"pane-1","expected_generation":999,"params":{"pane_id":"pane-1"}}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for stale generation", resp.StatusCode)
	}
}

func TestMutationClientDeadlinePassed(t *testing.T) {
	t.Parallel()
	now := time.Unix(2_000_000, 0)
	h := newHarness(t, withClock(func() time.Time { return now }))
	// deadline_unix_ms already in the past.
	past := now.Add(-time.Second).UnixMilli()
	body := `{"request_id":"rd","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7,"deadline_unix_ms":` +
		itoa(past) + `}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	if mr.Error == nil || mr.Error.Code != codeDeadlineExceeded {
		t.Fatalf("expected deadline_exceeded, got %+v", mr.Error)
	}
	if h.mutator.callCount() != 0 {
		t.Fatal("expired deadline must not dispatch")
	}
}

func TestBodyLimitEnforced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	big := `{"request_id":"r","operation":"pane.focus","params":{"x":"` + strings.Repeat("a", 2<<20) + `"}}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", big)
	var mr mutationResponse
	// MaxBytesReader triggers a decode error -> 400 bad_request.
	decodeBody(t, resp, &struct{}{})
	_ = mr
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, want 400", resp.StatusCode)
	}
}

func TestRateLimit(t *testing.T) {
	t.Parallel()
	now := time.Unix(3_000_000, 0)
	h := newHarness(t, withClock(func() time.Time { return now }))
	// Tighten the limiter for this server by rebuilding with burst 2.
	h.server.rl = newRateLimiter(time.Hour, 2, func() time.Time { return now })
	cookie, _ := h.sessionCookie()
	get := func() int {
		r := h.do(http.MethodGet, apiPrefix+"/snapshot", "", withCookie(cookie), withOrigin(h.origin))
		r.Body.Close()
		return r.StatusCode
	}
	_ = get()
	_ = get()
	if code := get(); code != http.StatusTooManyRequests {
		t.Fatalf("third request status = %d, want 429", code)
	}
}

func TestPaneReadNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/panes/ghost/read?source=visible")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ---- SPA fallback ---------------------------------------------------------

func TestSPAServesShell(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.do(http.MethodGet, "/some/client/route", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("SPA responses must carry CSP")
	}
	buf := make([]byte, 16)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "SHELL" {
		t.Fatalf("body = %q", string(buf[:n]))
	}
}

func TestUnknownAPIRouteIs404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.do(http.MethodGet, apiPrefix+"/does-not-exist", "", withOrigin(h.origin))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var ae apiError
	decodeBody(t, resp, &ae)
	if ae.Error.Code != codeNotFound {
		t.Fatalf("error code = %q", ae.Error.Code)
	}
}

func TestNewRejectsMissingDeps(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{PublicHost: "h"}, Deps{}); err == nil {
		t.Fatal("expected error for missing deps")
	}
	if _, err := New(Config{}, Deps{
		Auth: newFakeAuth(), State: newFakeState(), Mutator: &fakeMutator{},
		Daemon: fakeDaemon{}, Tunnel: fakeTunnel{}, Directories: fakeDirs{},
	}); err == nil {
		t.Fatal("expected error for missing host config")
	}
}

// ---- events websocket -----------------------------------------------------

func TestEventsStreamsInitialAndUpdates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, _ := h.sessionCookie()
	c, resp := h.dialWS(apiPrefix+"/events", cookie)
	if resp.Status != "101" {
		t.Fatalf("events handshake = %s", resp.Status)
	}
	defer c.close()

	// Initial snapshot.
	_, data := c.readFrame(t)
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Type != "snapshot" || env.Snapshot.Hash != "hash-1" {
		t.Fatalf("initial envelope = %+v", env)
	}

	// Push an update; the client should receive it.
	h.state.push(Snapshot{Version: 3, Hash: "hash-3", Data: json.RawMessage(`{}`)})
	_, data = c.readFrame(t)
	_ = json.Unmarshal(data, &env)
	if env.Snapshot.Hash != "hash-3" {
		t.Fatalf("update hash = %q, want hash-3", env.Snapshot.Hash)
	}
}

func TestEventsRequiresSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Bad cookie: handshake must not upgrade (middleware rejects before Accept).
	_, resp := h.dialWS(apiPrefix+"/events", "nope")
	if resp.Status == "101" {
		t.Fatal("events upgraded without a valid session")
	}
}

func TestConcurrentClientsAndMutationsRace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, csrf := h.sessionCookie()

	const pushes = 20
	latestHash := "h" + itoa(int64(pushes-1)) // the final coalesced snapshot

	var wg sync.WaitGroup

	// Several event WebSocket clients reading concurrently. The hub coalesces
	// consecutive snapshots to the newest (bounded 1-item queue, SPEC §11), so a
	// slow client legitimately receives fewer frames than were pushed. Assert the
	// contract that actually holds: each client sees at least one snapshot and,
	// bounded by a deadline, eventually observes the latest coalesced hash.
	const clients = 4
	type clientResult struct {
		frames int
		latest bool
	}
	results := make(chan clientResult, clients)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, resp := h.dialWS(apiPrefix+"/events", cookie)
			if resp.Status != "101" {
				results <- clientResult{}
				return
			}
			defer c.close()
			c.setReadDeadline(time.Now().Add(5 * time.Second))
			var res clientResult
			for {
				op, data, err := c.readFrameErr()
				if err != nil {
					break // deadline or close: stop reading
				}
				if op != 0x1 { // text frames only carry snapshots
					continue
				}
				var env envelope
				if json.Unmarshal(data, &env) != nil || env.Snapshot == nil {
					continue
				}
				res.frames++
				if env.Snapshot.Hash == latestHash {
					res.latest = true
					break
				}
			}
			results <- res
		}()
	}

	// A pusher driving snapshot fan-out through the hub.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < pushes; i++ {
			h.state.push(Snapshot{Version: i, Hash: "h" + itoa(int64(i)), Data: json.RawMessage(`{}`)})
			time.Sleep(time.Millisecond)
		}
	}()

	// Concurrent mutations exercising stores + middleware.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			body := `{"request_id":"race-` + itoa(int64(n)) + `","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`
			resp := h.do(http.MethodPost, apiPrefix+"/mutations", body,
				withCookie(cookie), withCSRF(csrf), withOrigin(h.origin), withHeader("Content-Type", "application/json"))
			resp.Body.Close()
		}(i)
	}

	wg.Wait()
	close(results)
	for res := range results {
		if res.frames < 1 {
			t.Errorf("event client received no snapshot frames")
		}
		if !res.latest {
			t.Errorf("event client never observed the latest coalesced hash %q (frames=%d)", latestHash, res.frames)
		}
	}
}

// ---- terminal websocket ---------------------------------------------------

func TestTerminalBridgeForwardsFrame(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, _ := h.sessionCookie()
	c, resp := h.dialWS(apiPrefix+"/terminals/pane-1?expected_generation=7", cookie)
	if resp.Status != "101" {
		t.Fatalf("terminal handshake = %s", resp.Status)
	}
	defer c.close()

	// Expect the decoded "hi" frame to arrive as a binary message. Skip any
	// text metadata frames (opened/resized/closed).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op, data := c.readFrame(t)
		if op == 0x2 { // binary
			if string(data) == "hi" {
				return
			}
		}
	}
	t.Fatal("did not receive decoded terminal frame")
}

func TestTerminalTakeoverRequiresConfirmation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, _ := h.sessionCookie()
	// takeover=1 with no confirmation nonce must be rejected before upgrade.
	_, resp := h.dialWS(apiPrefix+"/terminals/pane-1?takeover=1&expected_generation=7", cookie)
	if resp.Status == "101" {
		t.Fatal("takeover upgraded without confirmation")
	}
}

func TestTerminalTakeoverWithConfirmation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Obtain a takeover confirmation nonce bound to pane-1 at generation 7.
	confResp := h.authedJSON(http.MethodPost, apiPrefix+"/confirmations",
		`{"operation":"terminal.takeover","resource_id":"pane-1","expected_generation":7}`)
	var cr confirmationResponse
	decodeBody(t, confResp, &cr)
	if cr.Confirmation == "" {
		t.Fatal("expected takeover confirmation")
	}

	cookie := "live-session" // same session used by authedJSON
	c, resp := h.dialWS(apiPrefix+"/terminals/pane-1?takeover=1&expected_generation=7&confirmation="+cr.Confirmation, cookie)
	if resp.Status != "101" {
		t.Fatalf("takeover handshake with confirmation = %s", resp.Status)
	}
	defer c.close()
	// Launch happens asynchronously inside the bridge; wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.term.lastSpec().Takeover {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("launcher spec should carry takeover=true")
}
