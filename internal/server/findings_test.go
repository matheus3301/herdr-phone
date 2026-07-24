package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// H1 / TG2: a generation-checked op that omits expected_generation must be
// rejected and must never reach the mutator.
func TestMutationOmittedGenerationRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := `{"request_id":"g1","operation":"agent.send_keys","params":{"pane_id":"pane-1","keys":["a"]}}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	if mr.Error == nil || mr.Error.Code != codeGenerationStale {
		t.Fatalf("expected generation_stale for omitted generation, got %+v", mr.Error)
	}
	if mr.Error.Retryable {
		t.Error("omitted-generation rejection should not be retryable (client must supply it)")
	}
	if h.mutator.callCount() != 0 {
		t.Fatal("omitted generation must not dispatch to the mutator")
	}
}

// H1: terminal attach without expected_generation must be rejected before upgrade.
func TestTerminalAttachRequiresGeneration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cookie, _ := h.sessionCookie()
	_, resp := h.dialWS(apiPrefix+"/terminals/pane-1", cookie)
	if resp.Status == "101" {
		t.Fatal("terminal attach upgraded without expected_generation")
	}
}

// H2 / TG1: two concurrent requests with the same session+request_id must
// execute the mutation exactly once; the loser gets a retryable conflict.
func TestConcurrentIdenticalRequestsExecuteOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.mutator.enter = make(chan string, 4)
	h.mutator.hold = make(chan struct{})

	body := `{"request_id":"dup-1","operation":"pane.split","params":{"pane_id":"pane-1"},"expected_generation":7}`

	type outcome struct {
		accepted bool
		errCode  string
	}
	results := make(chan outcome, 2)
	send := func() {
		resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
		var mr mutationResponse
		decodeBody(t, resp, &mr)
		code := ""
		if mr.Error != nil {
			code = mr.Error.Code
		}
		results <- outcome{accepted: mr.Accepted, errCode: code}
	}

	// Request A enters Mutate and holds the reservation.
	go send()
	select {
	case <-h.mutator.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never reached the mutator")
	}

	// Request B, issued while A is in flight under the same key, must be told the
	// request is already in progress rather than double-executing.
	go send()

	var bOutcome outcome
	select {
	case bOutcome = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("second request did not return while first was held")
	}
	if bOutcome.accepted || bOutcome.errCode != codeConflict {
		t.Fatalf("concurrent duplicate outcome = %+v, want retryable conflict", bOutcome)
	}

	// Release A; it should succeed.
	close(h.mutator.hold)
	select {
	case a := <-results:
		if !a.accepted {
			t.Fatalf("held request outcome = %+v, want accepted", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held request never completed")
	}

	if h.mutator.callCount() != 1 {
		t.Fatalf("mutator executed %d times, want exactly 1", h.mutator.callCount())
	}
}

// H3: a retryable failure must not be cached, so a later retry re-attempts and
// can succeed.
func TestRetryableErrorNotCached(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(func(c *Config) {
		c.ServerMutationDeadline = 40 * time.Millisecond
	}))
	// First attempt: the mutator outruns the server deadline -> retryable timeout.
	h.mutator.setDelay(300 * time.Millisecond)

	body := `{"request_id":"retry-1","operation":"pane.split","params":{"pane_id":"pane-1"},"expected_generation":7}`
	r1 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr1 mutationResponse
	decodeBody(t, r1, &mr1)
	if mr1.Error == nil || mr1.Error.Code != codeDeadlineExceeded || !mr1.Error.Retryable {
		t.Fatalf("first attempt = %+v, want retryable deadline_exceeded", mr1.Error)
	}

	// Herdr recovers; the retry (same request_id) must actually re-attempt, not
	// replay the cached error.
	h.mutator.setDelay(0)
	r2 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	if r2.Header.Get("Idempotent-Replay") == "true" {
		r2.Body.Close()
		t.Fatal("retryable error was cached and replayed; retry never re-attempted")
	}
	var mr2 mutationResponse
	decodeBody(t, r2, &mr2)
	if !mr2.Accepted {
		t.Fatalf("retry outcome = %+v, want accepted", mr2)
	}
	if h.mutator.callCount() != 2 {
		t.Fatalf("mutator called %d times, want 2 (original + retry)", h.mutator.callCount())
	}
}

// M1: a divergent alternate identifier (agent target != pane_id) is rejected so
// the guard and dispatch key on the same resource.
func TestDivergentAgentTargetRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := `{"request_id":"m1","operation":"agent.prompt","params":{"pane_id":"pane-1","target":"pane-2","prompt":"hi"},"expected_generation":7}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	if mr.Error == nil || mr.Error.Code != codeBadRequest {
		t.Fatalf("expected bad_request for divergent target, got %+v", mr.Error)
	}
	if h.mutator.callCount() != 0 {
		t.Fatal("divergent target must not dispatch")
	}
}

// M1: a matching target (== pane_id) is allowed.
func TestMatchingAgentTargetAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := `{"request_id":"m1b","operation":"agent.prompt","params":{"pane_id":"pane-1","target":"pane-1","prompt":"hi"},"expected_generation":7}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	if !mr.Accepted {
		t.Fatalf("matching target should be accepted, got %+v", mr)
	}
}

// M1: worktree removal with a divergent workspace_id is rejected at confirmation.
func TestDivergentWorktreeWorkspaceRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := `{"operation":"worktree.remove","resource_id":"wt-1","params":{"worktree_id":"wt-1","workspace_id":"ws-9"}}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/confirmations", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("confirmation status = %d, want 400 for divergent workspace_id", resp.StatusCode)
	}
}

// CSP connect-src must be scoped to the configured origin's WebSocket, not the
// bare ws:/wss: scheme-wide allowance.
func TestCSPConnectSrcScopedToOrigin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/capabilities")
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	wantWS := "ws://" + h.addr // harness origin is http://<addr>
	if !strings.Contains(csp, "connect-src 'self' "+wantWS) {
		t.Fatalf("connect-src not scoped to origin: %q", csp)
	}
	// The scheme-wide allowance ("ws: wss:") must be gone.
	if strings.Contains(csp, "ws: wss:") {
		t.Fatalf("connect-src still uses scheme-wide allowance: %q", csp)
	}
}

// Fail-closed: with no terminal filter factory, the terminal route refuses to
// open rather than relaying unfiltered output.
func TestTerminalFailsClosedWithoutFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withNoTerminalFilter())
	cookie, _ := h.sessionCookie()
	_, resp := h.dialWS(apiPrefix+"/terminals/pane-1?expected_generation=7", cookie)
	if resp.Status == "101" {
		t.Fatal("terminal opened without an output filter (fail-open)")
	}
}

// L4: idle rate-limit buckets are evicted so the map cannot grow unbounded.
func TestRateLimiterEvictsIdleBuckets(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	rl := newRateLimiter(time.Millisecond, 5, clock)

	for _, k := range []string{"a", "b", "c"} {
		rl.allow(k)
	}
	rl.mu.Lock()
	before := len(rl.m)
	rl.mu.Unlock()
	if before != 3 {
		t.Fatalf("bucket count = %d, want 3", before)
	}

	// Advance past the idle TTL and the sweep interval, then touch a new key to
	// trigger the sweep.
	now = now.Add(rlIdleTTL + rlSweepInterval + time.Second)
	rl.allow("d")

	rl.mu.Lock()
	after := len(rl.m)
	_, dLive := rl.m["d"]
	rl.mu.Unlock()
	if !dLive {
		t.Fatal("freshly touched bucket was evicted")
	}
	if after != 1 {
		t.Fatalf("bucket count after sweep = %d, want 1 (idle a/b/c evicted)", after)
	}
}

// Ensure the idempotency reservation is released on a pre-dispatch validation
// failure so the key is not wedged for later legitimate use.
func TestReservationNotLeakedOnValidationFailure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// First: stale generation (mismatch) — a pre-dispatch rejection.
	bad := `{"request_id":"res-1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":3}`
	r1 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", bad)
	r1.Body.Close()

	// Same request_id with a correct generation must proceed (not be blocked as
	// an in-flight duplicate or replayed).
	good := `{"request_id":"res-1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`
	r2 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", good)
	if r2.Header.Get("Idempotent-Replay") == "true" {
		r2.Body.Close()
		t.Fatal("a rejected pre-dispatch request left a cached entry")
	}
	var mr mutationResponse
	decodeBody(t, r2, &mr)
	if !mr.Accepted {
		t.Fatalf("retry after validation failure = %+v, want accepted", mr)
	}
}

// R2: on a retryable dispatch failure of a confirmation-required op, the spent
// single-use nonce means a plain retry cannot succeed, so the server must return
// an explicit re-confirm outcome (confirmation_required, not retryable) rather
// than claiming the same request can be retried.
func TestDestructiveRetryableFailureAsksReconfirm(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(func(c *Config) {
		c.ServerMutationDeadline = 40 * time.Millisecond
	}))
	// The mutator outruns the server deadline -> a retryable timeout after the
	// nonce has been consumed.
	h.mutator.setDelay(300 * time.Millisecond)

	confResp := h.authedJSON(http.MethodPost, apiPrefix+"/confirmations",
		`{"operation":"pane.close","resource_id":"pane-1","expected_generation":7,"params":{"pane_id":"pane-1"}}`)
	var cr confirmationResponse
	decodeBody(t, confResp, &cr)
	if cr.Confirmation == "" {
		t.Fatal("expected confirmation nonce")
	}

	body := `{"request_id":"rc-1","operation":"pane.close","params":{"pane_id":"pane-1"},"expected_generation":7,"confirmation":"` + cr.Confirmation + `"}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	if mr.Error == nil || mr.Error.Code != codeConfirmationNeeded {
		t.Fatalf("expected confirmation_required reconfirm outcome, got %+v", mr.Error)
	}
	if mr.Error.Retryable {
		t.Error("reconfirm outcome must not be marked retryable (nonce is spent)")
	}
}

// R2: a confirmation-required op rejected BEFORE dispatch (here, the client
// deadline already passed) must leave the single-use nonce intact, so a later
// attempt with the same nonce still succeeds.
func TestNonceIntactAfterPreDispatchRejection(t *testing.T) {
	t.Parallel()
	now := time.Unix(2_500_000, 0)
	h := newHarness(t, withClock(func() time.Time { return now }))

	confResp := h.authedJSON(http.MethodPost, apiPrefix+"/confirmations",
		`{"operation":"pane.close","resource_id":"pane-1","expected_generation":7,"params":{"pane_id":"pane-1"}}`)
	var cr confirmationResponse
	decodeBody(t, confResp, &cr)
	if cr.Confirmation == "" {
		t.Fatal("expected confirmation nonce")
	}

	// First attempt: client deadline already in the past -> rejected before the
	// nonce is consumed.
	past := now.Add(-time.Second).UnixMilli()
	first := `{"request_id":"rc-2","operation":"pane.close","params":{"pane_id":"pane-1"},"expected_generation":7,"confirmation":"` + cr.Confirmation + `","deadline_unix_ms":` + itoa(past) + `}`
	r1 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", first)
	var mr1 mutationResponse
	decodeBody(t, r1, &mr1)
	if mr1.Error == nil || mr1.Error.Code != codeDeadlineExceeded {
		t.Fatalf("first attempt = %+v, want deadline_exceeded", mr1.Error)
	}
	if h.mutator.callCount() != 0 {
		t.Fatal("pre-dispatch rejection must not dispatch")
	}

	// Second attempt with the same (still-valid) nonce must consume it and succeed.
	second := `{"request_id":"rc-3","operation":"pane.close","params":{"pane_id":"pane-1"},"expected_generation":7,"confirmation":"` + cr.Confirmation + `"}`
	r2 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", second)
	var mr2 mutationResponse
	decodeBody(t, r2, &mr2)
	if !mr2.Accepted {
		t.Fatalf("second attempt with intact nonce = %+v, want accepted", mr2)
	}
}

// R3: agent.start ignores "target" in its dispatcher, so a divergent target must
// be accepted (no longer rejected as a conflicting resource identifier).
func TestAgentStartAllowsDivergentTarget(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := `{"request_id":"as-1","operation":"agent.start","params":{"pane_id":"pane-1","target":"pane-2","kind":"claude"},"expected_generation":7}`
	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	if !mr.Accepted {
		t.Fatalf("agent.start with divergent target should be accepted, got %+v", mr)
	}
	if h.mutator.lastOp() != "agent.start" {
		t.Fatalf("mutator op = %q, want agent.start", h.mutator.lastOp())
	}
}

// Rate limiting: a flood on unauthenticated /health must not starve /pair; each
// unauthenticated route has its own bucket.
func TestUnauthRateBucketsSeparatedByRoute(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(func(c *Config) {
		c.RateBurst = 2
		c.RateEvery = time.Hour // no meaningful refill during the test
	}))

	// Exhaust the /health bucket.
	var got429 bool
	for i := 0; i < 5; i++ {
		resp := h.do(http.MethodGet, "/health", "")
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
		}
	}
	if !got429 {
		t.Fatal("expected /health to be rate limited after exhausting its bucket")
	}

	// /pair must still be reachable (its own bucket): a wrong secret reaches the
	// handler and returns 401, not 429.
	pair := h.do(http.MethodPost, apiPrefix+"/pair", `{"secret":"wrong"}`,
		withOrigin(h.origin), withHeader("Content-Type", "application/json"))
	pair.Body.Close()
	if pair.StatusCode == http.StatusTooManyRequests {
		t.Fatal("/pair was starved by the /health flood (shared rate bucket)")
	}
	if pair.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/pair status = %d, want 401 (reached handler)", pair.StatusCode)
	}
}
