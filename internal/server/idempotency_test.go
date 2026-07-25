package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A request id is chosen by the client. Binding each idempotency entry to a
// fingerprint of the operation, the asserted generation, and the normalized
// params means a reused id can never retrieve a response belonging to a
// different payload.
func TestRequestIDReuseWithDifferentPayloadRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	first := `{"request_id":"dup-1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`
	r1 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", first)
	var mr1 mutationResponse
	decodeBody(t, r1, &mr1)
	if !mr1.Accepted {
		t.Fatalf("first request = %+v, want accepted", mr1)
	}

	// Same id, different operation.
	second := `{"request_id":"dup-1","operation":"pane.zoom","params":{"pane_id":"pane-1"},"expected_generation":7}`
	r2 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", second)
	if r2.Header.Get("Idempotent-Replay") == "true" {
		r2.Body.Close()
		t.Fatal("a different operation must not replay the cached response")
	}
	var mr2 mutationResponse
	decodeBody(t, r2, &mr2)
	if mr2.Error == nil || mr2.Error.Code != codeConflict || mr2.Error.Retryable {
		t.Fatalf("reused id with a different operation = %+v, want non-retryable conflict", mr2.Error)
	}

	// Same id and operation, different params.
	third := `{"request_id":"dup-1","operation":"pane.focus","params":{"pane_id":"pane-other"},"expected_generation":7}`
	r3 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", third)
	var mr3 mutationResponse
	decodeBody(t, r3, &mr3)
	if mr3.Error == nil || mr3.Error.Code != codeConflict {
		t.Fatalf("reused id with different params = %+v, want conflict", mr3.Error)
	}

	// Only the original request replays.
	r4 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", first)
	defer r4.Body.Close()
	if r4.Header.Get("Idempotent-Replay") != "true" {
		t.Fatal("the original payload must still replay")
	}
	if h.mutator.callCount() != 1 {
		t.Fatalf("mutator called %d times, want exactly 1", h.mutator.callCount())
	}
}

// A replay skips the generation guard, so the asserted generation is part of the
// binding: a cached success must never be handed back for a generation that was
// never validated.
func TestRequestIDReuseWithDifferentGenerationRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := `{"request_id":"gen-1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`
	r1 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
	var mr1 mutationResponse
	decodeBody(t, r1, &mr1)
	if !mr1.Accepted {
		t.Fatalf("first request = %+v", mr1)
	}

	other := `{"request_id":"gen-1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":8}`
	r2 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", other)
	if r2.Header.Get("Idempotent-Replay") == "true" {
		r2.Body.Close()
		t.Fatal("a different asserted generation must not replay the cached response")
	}
	var mr2 mutationResponse
	decodeBody(t, r2, &mr2)
	if mr2.Error == nil || mr2.Error.Code != codeConflict {
		t.Fatalf("reused id with a different generation = %+v, want conflict", mr2.Error)
	}
}

// Key ordering and whitespace are not payload differences: the fingerprint is
// built from canonicalized params, so a re-serialized identical retry still
// replays exactly once.
func TestReorderedParamsStillReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := `{"request_id":"norm-1","operation":"pane.resize","params":{"pane_id":"pane-1","direction":"left","amount":2},"expected_generation":7}`
	b := `{"request_id":"norm-1","operation":"pane.resize","params":{"amount":2,  "direction":"left","pane_id":"pane-1"},"expected_generation":7}`

	r1 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", a)
	var mr1 mutationResponse
	decodeBody(t, r1, &mr1)
	if !mr1.Accepted {
		t.Fatalf("first request = %+v", mr1)
	}
	r2 := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", b)
	defer r2.Body.Close()
	if r2.Header.Get("Idempotent-Replay") != "true" {
		t.Fatal("a re-serialized identical retry must replay")
	}
	if h.mutator.callCount() != 1 {
		t.Fatalf("mutator called %d times, want 1", h.mutator.callCount())
	}
}

// A reservation held by an in-flight request must reject a different payload
// under the same id rather than report it as a duplicate in progress.
func TestInFlightReservationRejectsDifferentPayload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	enter := make(chan string, 1)
	hold := make(chan struct{})
	h.mutator.mu.Lock()
	h.mutator.enter = enter
	h.mutator.hold = hold
	h.mutator.mu.Unlock()

	go func() {
		resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations",
			`{"request_id":"flight-1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`)
		resp.Body.Close()
	}()
	select {
	case <-enter:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never reached the mutator")
	}

	resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations",
		`{"request_id":"flight-1","operation":"pane.zoom","params":{"pane_id":"pane-1"},"expected_generation":7}`)
	var mr mutationResponse
	decodeBody(t, resp, &mr)
	close(hold)
	if mr.Error == nil || mr.Error.Code != codeConflict || mr.Error.Retryable {
		t.Fatalf("different payload under an in-flight id = %+v, want non-retryable conflict", mr.Error)
	}
}

// The idempotency store must not leak across sessions: the key includes the
// session id, so two sessions using the same request id are independent.
func TestIdempotencyKeyedBySession(t *testing.T) {
	t.Parallel()
	store := newIdemStore(func() time.Time { return time.Unix(1000, 0) })
	if a, b := idemKey("s1", "r1"), idemKey("s2", "r1"); a == b {
		t.Fatal("keys must differ by session")
	}
	if _, res := store.reserve(idemKey("s1", "r1"), "fp", time.Minute); res != idemReserved {
		t.Fatalf("first reserve = %v", res)
	}
	if _, res := store.reserve(idemKey("s2", "r1"), "fp", time.Minute); res != idemReserved {
		t.Fatalf("another session must reserve independently, got %v", res)
	}
}

func TestIdemStorePeekAndReserveFingerprints(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := newIdemStore(func() time.Time { return now })
	key := idemKey("s", "r")

	if _, res := store.peek(key, "fp-a"); res != idemMiss {
		t.Fatalf("empty peek = %v, want miss", res)
	}
	if _, res := store.reserve(key, "fp-a", time.Minute); res != idemReserved {
		t.Fatalf("reserve = %v", res)
	}
	if _, res := store.peek(key, "fp-a"); res != idemMiss {
		t.Fatalf("pending peek with matching fingerprint = %v, want miss", res)
	}
	if _, res := store.peek(key, "fp-b"); res != idemMismatch {
		t.Fatalf("pending peek with a different fingerprint = %v, want mismatch", res)
	}
	if _, res := store.reserve(key, "fp-a", time.Minute); res != idemInFlight {
		t.Fatalf("duplicate reserve = %v, want in-flight", res)
	}
	if _, res := store.reserve(key, "fp-b", time.Minute); res != idemMismatch {
		t.Fatalf("reserve with a different fingerprint = %v, want mismatch", res)
	}

	store.complete(key, "fp-a", http.StatusOK, []byte(`{"ok":true}`), time.Minute)
	if e, res := store.peek(key, "fp-a"); res != idemDone || string(e.body) != `{"ok":true}` {
		t.Fatalf("completed peek = %v / %q", res, e.body)
	}
	if _, res := store.peek(key, "fp-b"); res != idemMismatch {
		t.Fatalf("completed peek with a different fingerprint = %v, want mismatch", res)
	}
	if _, res := store.reserve(key, "fp-b", time.Minute); res != idemMismatch {
		t.Fatalf("completed reserve with a different fingerprint = %v, want mismatch", res)
	}

	// An expired entry frees the id for any payload.
	now = now.Add(2 * time.Minute)
	if _, res := store.peek(key, "fp-b"); res != idemMiss {
		t.Fatalf("expired peek = %v, want miss", res)
	}
}

func TestRequestFingerprintDistinguishesRequests(t *testing.T) {
	t.Parallel()
	base := requestFingerprint("pane.close", 7, []byte(`{"pane_id":"p1"}`))
	cases := map[string]string{
		"different operation":  requestFingerprint("pane.focus", 7, []byte(`{"pane_id":"p1"}`)),
		"different generation": requestFingerprint("pane.close", 8, []byte(`{"pane_id":"p1"}`)),
		"different params":     requestFingerprint("pane.close", 7, []byte(`{"pane_id":"p2"}`)),
	}
	for what, got := range cases {
		if got == base {
			t.Errorf("%s must change the fingerprint", what)
		}
	}
	if requestFingerprint("pane.close", 7, []byte(`{"pane_id":"p1"}`)) != base {
		t.Error("an identical request must fingerprint identically")
	}
	// A field boundary must not be forgeable by shifting content between fields.
	if requestFingerprint("a", 1, []byte("b")) == requestFingerprint("a\x001", 0, []byte("b")) {
		t.Error("fingerprint fields must not be ambiguous")
	}
}

// ---- structured upstream error preservation --------------------------------

// Flattening every operation failure into one code would tell the client to
// retry things that can never succeed and would make each cause undebuggable.
// Distinct upstream codes must stay distinct, with static messages only.
func TestMutationPreservesStructuredUpstreamErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		upstream      string
		wantCode      string
		wantStatus    int
		wantRetryable bool
		wantCached    bool
	}{
		{upstreamNotFound, codeNotFound, http.StatusNotFound, false, true},
		{upstreamInvalidParams, codeBadRequest, http.StatusBadRequest, false, true},
		{upstreamInvalidRequest, codeBadRequest, http.StatusBadRequest, false, true},
		{upstreamFeatureDisabled, codeUnsupported, http.StatusNotImplemented, false, true},
		{upstreamPlatformUnsupported, codeUnsupported, http.StatusNotImplemented, false, true},
		{upstreamPluginDisabled, codeUnsupported, http.StatusNotImplemented, false, true},
		{upstreamIncompatible, codeUnsupported, http.StatusNotImplemented, false, true},
		{upstreamStreamConflict, codeConflict, http.StatusConflict, false, true},
		{upstreamTimeout, codeDeadlineExceeded, http.StatusGatewayTimeout, true, false},
		{upstreamConnect, codeUnavailable, http.StatusServiceUnavailable, true, false},
		{upstreamTransport, codeUnavailable, http.StatusServiceUnavailable, true, false},
		{upstreamCanceled, codeUnavailable, http.StatusServiceUnavailable, true, false},
		{"some_future_code", codeInternal, http.StatusBadGateway, false, true},
	}
	for _, tc := range cases {
		h := newHarness(t)
		h.mutator.mu.Lock()
		h.mutator.err = upstreamErr{tc.upstream}
		h.mutator.mu.Unlock()

		body := `{"request_id":"e-1","operation":"pane.focus","params":{"pane_id":"pane-1"},"expected_generation":7}`
		resp := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
		status := resp.StatusCode
		var mr mutationResponse
		decodeBody(t, resp, &mr)

		if mr.Error == nil {
			t.Errorf("%s: no error returned", tc.upstream)
			continue
		}
		if mr.Error.Code != tc.wantCode || status != tc.wantStatus {
			t.Errorf("%s = %d/%s, want %d/%s", tc.upstream, status, mr.Error.Code, tc.wantStatus, tc.wantCode)
		}
		if mr.Error.Retryable != tc.wantRetryable {
			t.Errorf("%s: retryable = %v, want %v", tc.upstream, mr.Error.Retryable, tc.wantRetryable)
		}
		// Only static messages may leave the relay: an upstream message can quote
		// pane content, a path, or a command.
		if mr.Error.Message != mutationMessage(tc.wantCode) {
			t.Errorf("%s: message = %q, want the static %q", tc.upstream, mr.Error.Message, mutationMessage(tc.wantCode))
		}
		if mr.Error.Message == "" || strings.Contains(mr.Error.Message, "upstream") {
			t.Errorf("%s: message leaked upstream text: %q", tc.upstream, mr.Error.Message)
		}

		// A retryable failure must stay retryable: never cached.
		replay := h.authedJSON(http.MethodPost, apiPrefix+"/mutations", body)
		replay.Body.Close()
		cached := replay.Header.Get("Idempotent-Replay") == "true"
		if cached != tc.wantCached {
			t.Errorf("%s: cached = %v, want %v", tc.upstream, cached, tc.wantCached)
		}
	}
}

func TestClassifyMutateErrContextCauseWins(t *testing.T) {
	t.Parallel()
	// A relay-side deadline is authoritative even when the upstream also reported
	// a code: the client's decision depends on how *we* gave up.
	expired, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	code, status, retryable := classifyMutateErr(expired, upstreamErr{upstreamNotFound})
	if code != codeDeadlineExceeded || status != http.StatusGatewayTimeout || !retryable {
		t.Fatalf("deadline = %s/%d/%v", code, status, retryable)
	}

	canceled, cancel2 := context.WithCancel(context.Background())
	cancel2()
	code, status, retryable = classifyMutateErr(canceled, upstreamErr{upstreamNotFound})
	if code != codeUnavailable || status != http.StatusServiceUnavailable || !retryable {
		t.Fatalf("canceled = %s/%d/%v", code, status, retryable)
	}

	// A code-free error stays an unclassified upstream fault.
	code, status, retryable = classifyMutateErr(context.Background(), errors.New("boom"))
	if code != codeInternal || status != http.StatusBadGateway || retryable {
		t.Fatalf("plain error = %s/%d/%v", code, status, retryable)
	}
}

func TestUpstreamCodeUnwrapsWrappedErrors(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("dispatch failed: %w", upstreamErr{upstreamNotFound})
	if got := upstreamCode(wrapped); got != upstreamNotFound {
		t.Fatalf("upstreamCode(wrapped) = %q, want %q", got, upstreamNotFound)
	}
	if got := upstreamCode(errors.New("plain")); got != "" {
		t.Fatalf("upstreamCode(plain) = %q, want empty", got)
	}
	if got := upstreamCode(nil); got != "" {
		t.Fatalf("upstreamCode(nil) = %q, want empty", got)
	}
}
