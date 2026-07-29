package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// upstreamErr is an error carrying a structured upstream code, as the Herdr
// adapter's *herdr.Error does.
type upstreamErr struct{ code string }

func (e upstreamErr) Error() string        { return "upstream: " + e.code }
func (e upstreamErr) UpstreamCode() string { return e.code }

// decodedRunResponse decodes a run response for assertions.
//
// The production runResponse carries Parts as []any because the contract is a
// heterogeneous typed-part list (SPEC §12.1/§12.2). Tests that only care about the
// always-present observed-output part decode into this typed shape instead; the
// interpreted-part tests decode into their own shapes.
type decodedRunResponse struct {
	ContractVersion int                  `json:"contract_version"`
	Capabilities    runCapabilities      `json:"capabilities"`
	Run             RunSummary           `json:"run"`
	Parts           []observedOutputPart `json:"parts"`
}

// ---- wire contract --------------------------------------------------------

// The run inbox wire shape is a contract the browser fails closed against, so it
// is asserted field by field rather than by round-tripping through the Go type.
func TestRunsListExactWire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/runs")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	var got map[string]any
	decodeBody(t, resp, &got)

	if got["contract_version"] != float64(runContractVersion) {
		t.Errorf("contract_version = %v, want %d", got["contract_version"], runContractVersion)
	}
	if got["snapshot_hash"] != "hash-1" {
		t.Errorf("snapshot_hash = %v", got["snapshot_hash"])
	}
	if got["truncated"] != false {
		t.Errorf("truncated = %v, want false", got["truncated"])
	}

	runs, ok := got["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs = %v", got["runs"])
	}
	run, _ := runs[0].(map[string]any)
	want := map[string]any{
		"run_id":            "pane-1@7#0123456789abcdef",
		"pane_id":           "pane-1",
		"pane_generation":   float64(7),
		"agent_incarnation": "0123456789abcdef",
		"workspace_id":      "w1",
		"workspace_label":   "space-api",
		"tab_id":            "w1:t1",
		"tab_label":         "agents",
		"terminal_id":       "term-1",
		"agent_kind":        "claude",
		"agent_name":        "auth",
		"display_agent":     "Claude Code",
		"title":             "Fix auth refresh",
		"status":            "blocked",
		"interactive_ready": true,
		"launch_pending":    false,
		"focused":           false,
		"cwd":               "/code/space-api",
		"foreground_cwd":    "/code/space-api",
		"revision":          float64(42),
		"state_change_seq":  float64(9),
	}
	for k, v := range want {
		if run[k] != v {
			t.Errorf("run[%q] = %v (%T), want %v", k, run[k], run[k], v)
		}
	}
	wt, ok := run["worktree"].(map[string]any)
	if !ok {
		t.Fatalf("worktree = %v", run["worktree"])
	}
	for k, v := range map[string]any{
		"repo_name":          "space-api",
		"repo_root":          "/code/space-api",
		"checkout_path":      "/code/space-api-auth",
		"is_linked_worktree": true,
	} {
		if wt[k] != v {
			t.Errorf("worktree[%q] = %v, want %v", k, wt[k], v)
		}
	}
	// The inbox must never carry output, not even an empty part list.
	for _, forbidden := range []string{"parts", "text", "output", "messages", "transcript"} {
		if _, present := run[forbidden]; present {
			t.Errorf("run summary must not contain %q", forbidden)
		}
		if _, present := got[forbidden]; present {
			t.Errorf("runs response must not contain %q", forbidden)
		}
	}
}

func TestRunDetailExactWire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	var got struct {
		ContractVersion int             `json:"contract_version"`
		Capabilities    runCapabilities `json:"capabilities"`
		Run             RunSummary      `json:"run"`
		Parts           []struct {
			Type      string `json:"type"`
			Source    string `json:"source"`
			Format    string `json:"format"`
			Lines     int    `json:"lines"`
			Bytes     int    `json:"bytes"`
			Truncated bool   `json:"truncated"`
			Text      string `json:"text"`
		} `json:"parts"`
	}
	decodeBody(t, resp, &got)

	if got.ContractVersion != runContractVersion {
		t.Errorf("contract_version = %d", got.ContractVersion)
	}
	if got.Run.PaneID != "pane-1" || got.Run.PaneGeneration != 7 {
		t.Errorf("run identity = %s@%d", got.Run.PaneID, got.Run.PaneGeneration)
	}
	if len(got.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(got.Parts))
	}
	p := got.Parts[0]
	if p.Type != partObservedTerminalOutput {
		t.Errorf("part type = %q, want %q", p.Type, partObservedTerminalOutput)
	}
	if p.Source != "recent-unwrapped" || p.Format != "text" {
		t.Errorf("part source/format = %q/%q", p.Source, p.Format)
	}
	if p.Text != "last visible output" {
		t.Errorf("part text = %q", p.Text)
	}
	if p.Bytes != len("last visible output") || p.Truncated {
		t.Errorf("part bytes = %d truncated = %v", p.Bytes, p.Truncated)
	}
	// Review LOW 2: `lines` is what the response actually carries, not the bound
	// that was requested. The fake pane renders one line, so reporting
	// defaultRunOutputLines here would be a statement the UI repeats as fact.
	if p.Lines != 1 {
		t.Errorf("part lines = %d, want 1 (the lines actually returned)", p.Lines)
	}
}

// The requested bound is clamped and is what the upstream read is asked for; the
// reported line count is what came back. Both matter, and they are not the same
// number.
func TestRunOutputReportsReturnedLinesNotTheRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.state.setContent("pane-1", []byte("one\ntwo\nthree\n"))
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7&lines=200")
	var got decodedRunResponse
	decodeBody(t, resp, &got)
	if got.Parts[0].Lines != 3 {
		t.Fatalf("lines = %d, want 3", got.Parts[0].Lines)
	}
	if n := h.state.lastReadLines(); n != 200 {
		t.Fatalf("upstream asked for %d lines, want 200", n)
	}
}

// ---- capability advertisement ---------------------------------------------

// Herdr v0.7.5 has no semantic conversation surface. The relay must say so on
// both the capability document and every run response, so a client that fails
// closed never has to guess.
func TestRunCapabilitiesAdvertiseNoSemanticStructure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	check := func(what string, caps runCapabilities) {
		if caps.ContractVersion != runContractVersion {
			t.Errorf("%s: contract_version = %d", what, caps.ContractVersion)
		}
		if !caps.Supported || !caps.ObservedTerminalOutput {
			t.Errorf("%s: observed terminal output must be supported: %+v", what, caps)
		}
		if caps.StructuredMessages || caps.StructuredToolCalls || caps.StructuredInteractions ||
			caps.StructuredDiffs || caps.StructuredTests || caps.StructuredPlans {
			t.Errorf("%s: no semantic capability may be advertised: %+v", what, caps)
		}
		if len(caps.PartTypes) != 1 || caps.PartTypes[0] != partObservedTerminalOutput {
			t.Errorf("%s: part_types = %v", what, caps.PartTypes)
		}
		if caps.MaxOutputBytes <= 0 || caps.MaxOutputLines <= 0 || caps.MaxRuns <= 0 {
			t.Errorf("%s: bounds must be advertised: %+v", what, caps)
		}
		for _, src := range caps.OutputSources {
			if !paneReadSources[src] {
				t.Errorf("%s: advertised source %q is not accepted by the read allowlist", what, src)
			}
		}
	}

	capsResp := h.authedGET(apiPrefix + "/capabilities")
	var doc struct {
		Runs   runCapabilities `json:"runs"`
		Limits limitsJSON      `json:"limits"`
	}
	decodeBody(t, capsResp, &doc)
	check("capabilities", doc.Runs)
	if doc.Limits.MaxRunOutputBytes <= 0 || doc.Limits.MaxRunOutputLines <= 0 || doc.Limits.MaxRuns <= 0 {
		t.Errorf("limits must advertise run bounds: %+v", doc.Limits)
	}

	runResp := h.authedGET(apiPrefix + "/runs")
	var list struct {
		Capabilities runCapabilities `json:"capabilities"`
	}
	decodeBody(t, runResp, &list)
	check("runs list", list.Capabilities)

	detailResp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var detail struct {
		Capabilities runCapabilities `json:"capabilities"`
	}
	decodeBody(t, detailResp, &detail)
	check("run detail", detail.Capabilities)
}

// ---- generation guard ------------------------------------------------------

func TestRunReadRequiresGeneration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, q := range []string{"", "?expected_generation=", "?expected_generation=0", "?expected_generation=abc"} {
		resp := h.authedGET(apiPrefix + "/runs/pane-1" + q)
		var body apiError
		decodeBody(t, resp, &body)
		if resp.StatusCode != http.StatusBadRequest || body.Error.Code != codeGenerationStale {
			t.Errorf("query %q = %d/%s, want 400/%s", q, resp.StatusCode, body.Error.Code, codeGenerationStale)
		}
	}
}

func TestRunReadStaleGenerationRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=6")
	var body apiError
	decodeBody(t, resp, &body)
	if resp.StatusCode != http.StatusConflict || body.Error.Code != codeGenerationStale {
		t.Fatalf("status = %d code = %s, want 409/%s", resp.StatusCode, body.Error.Code, codeGenerationStale)
	}
}

func TestRunReadGonePaneRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.authedGET(apiPrefix + "/runs/pane-gone?expected_generation=7")
	var body apiError
	decodeBody(t, resp, &body)
	if resp.StatusCode != http.StatusConflict || body.Error.Code != codeGenerationStale {
		t.Fatalf("status = %d code = %s, want 409/%s", resp.StatusCode, body.Error.Code, codeGenerationStale)
	}
}

// A stale generation must be rejected before any upstream read happens.
func TestStaleRunReadDoesNotTouchHerdr(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.state.setReadErr(errors.New("must not be called"))
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var body apiError
	decodeBody(t, resp, &body)
	if body.Error.Code != codeGenerationStale {
		t.Fatalf("code = %s, want %s", body.Error.Code, codeGenerationStale)
	}
}

// A live, guarded pane with no agent occupant is a distinct outcome from a stale
// generation: the client must be told the run is gone, not that its state is old.
func TestRunUnavailableWhenNoRunOccupiesPane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.state.setRuns(nil)
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var body apiError
	decodeBody(t, resp, &body)
	if resp.StatusCode != http.StatusNotFound || body.Error.Code != codeRunUnavailable {
		t.Fatalf("status = %d code = %s, want 404/%s", resp.StatusCode, body.Error.Code, codeRunUnavailable)
	}
}

// A run whose projected generation disagrees with the asserted one (the pane was
// replaced between the guard and the projection read) must fail closed.
func TestRunProjectionGenerationRaceFailsClosed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.state.setRuns([]RunSummary{{PaneID: "pane-1", PaneGeneration: 8}})
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var body apiError
	decodeBody(t, resp, &body)
	if resp.StatusCode != http.StatusConflict || body.Error.Code != codeGenerationStale {
		t.Fatalf("status = %d code = %s, want 409/%s", resp.StatusCode, body.Error.Code, codeGenerationStale)
	}
}

// ---- read failure classification ------------------------------------------

func TestRunReadErrorCodesStayDistinct(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err        error
		wantCode   string
		wantStatus int
	}{
		{upstreamErr{upstreamNotFound}, codeRunUnavailable, http.StatusNotFound},
		{upstreamErr{upstreamInvalidParams}, codeBadRequest, http.StatusBadRequest},
		{upstreamErr{upstreamFeatureDisabled}, codeUnsupported, http.StatusNotImplemented},
		{upstreamErr{upstreamTimeout}, codeDeadlineExceeded, http.StatusGatewayTimeout},
		{upstreamErr{upstreamConnect}, codeUnavailable, http.StatusServiceUnavailable},
		{upstreamErr{upstreamTransport}, codeUnavailable, http.StatusServiceUnavailable},
		{errors.New("no structured code"), codeRunReadFailed, http.StatusBadGateway},
	}
	for _, tc := range cases {
		h := newHarness(t)
		h.state.setReadErr(tc.err)
		resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
		var body apiError
		decodeBody(t, resp, &body)
		if resp.StatusCode != tc.wantStatus || body.Error.Code != tc.wantCode {
			t.Errorf("%v = %d/%s, want %d/%s", tc.err, resp.StatusCode, body.Error.Code, tc.wantStatus, tc.wantCode)
		}
		// The upstream message must never be forwarded.
		if strings.Contains(body.Error.Message, "upstream") || strings.Contains(body.Error.Message, "structured") {
			t.Errorf("error message leaked upstream text: %q", body.Error.Message)
		}
	}
}

// ---- bounds ----------------------------------------------------------------

func TestRunOutputByteBoundKeepsTail(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(func(c *Config) { c.MaxRunOutputBytes = 32 }))
	h.state.setContent("pane-1", []byte(strings.Repeat("a", 100)+"TAILMARKER"))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var got decodedRunResponse
	decodeBody(t, resp, &got)
	p := got.Parts[0]
	if !p.Truncated {
		t.Error("oversized output must be reported as truncated")
	}
	if len(p.Text) > 32 {
		t.Errorf("text = %d bytes, want <= 32", len(p.Text))
	}
	if !strings.HasSuffix(p.Text, "TAILMARKER") {
		t.Errorf("truncation must keep the most recent tail, got %q", p.Text)
	}
	if p.Bytes != len(p.Text) {
		t.Errorf("bytes = %d, want %d", p.Bytes, len(p.Text))
	}
}

func TestRunOutputLinesClamped(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(func(c *Config) { c.MaxRunOutputLines = 50 }))
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7&lines=100000")
	var got decodedRunResponse
	decodeBody(t, resp, &got)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// The clamp applies to what the relay asks Herdr for. An unbounded request
	// must never reach the upstream read.
	if n := h.state.lastReadLines(); n != 50 {
		t.Fatalf("upstream asked for %d lines, want clamped to 50", n)
	}
}

func TestRunOutputRejectsBadSourceAndLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, q := range []string{
		"?expected_generation=7&source=detection",
		"?expected_generation=7&source=../etc",
		"?expected_generation=7&lines=0",
		"?expected_generation=7&lines=-4",
		"?expected_generation=7&lines=abc",
	} {
		resp := h.authedGET(apiPrefix + "/runs/pane-1" + q)
		var body apiError
		decodeBody(t, resp, &body)
		if resp.StatusCode != http.StatusBadRequest || body.Error.Code != codeBadRequest {
			t.Errorf("query %q = %d/%s, want 400/bad_request", q, resp.StatusCode, body.Error.Code)
		}
	}
}

func TestRunsListBoundedByMaxRuns(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withConfig(func(c *Config) { c.MaxRuns = 2 }))
	h.state.setRuns([]RunSummary{
		{PaneID: "p1", PaneGeneration: 1}, {PaneID: "p2", PaneGeneration: 1},
		{PaneID: "p3", PaneGeneration: 1}, {PaneID: "p4", PaneGeneration: 1},
	})
	resp := h.authedGET(apiPrefix + "/runs")
	var got runsResponse
	decodeBody(t, resp, &got)
	if len(got.Runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(got.Runs))
	}
	if !got.Truncated {
		t.Fatal("a bounded list must report truncated, not read as complete")
	}
}

// ---- sanitization ----------------------------------------------------------

// Labels, titles, and paths come from upstream and can carry control characters.
// They must never reach the wire able to steer a terminal or a log sink.
func TestRunDisplayFieldsSanitized(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.state.setRuns([]RunSummary{{
		PaneID:         "pane-1",
		PaneGeneration: 7,
		Title:          "hi\x1b]0;pwned\x07there\nsecond",
		WorkspaceLabel: "ws\x1b[31m",
		AgentName:      "a\x00b",
		CWD:            "/code\x07/x",
		Worktree:       &RunWorktree{RepoName: "r\x1bx", CheckoutPath: "/p\x1b[2J"},
	}})
	resp := h.authedGET(apiPrefix + "/runs")
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(raw)
	if strings.ContainsAny(body, "\x1b\x00\x07") {
		t.Fatalf("control characters survived into the run wire: %q", body)
	}

	var got runsResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	run := got.Runs[0]
	if strings.Contains(run.Title, "\n") {
		t.Errorf("a single-line title must not carry a newline: %q", run.Title)
	}
	if run.Worktree == nil || strings.ContainsAny(run.Worktree.RepoName+run.Worktree.CheckoutPath, "\x1b") {
		t.Errorf("worktree fields unsanitized: %+v", run.Worktree)
	}
}

// Observed output keeps its line structure but loses every terminal control, so
// a repaint sequence cannot rewrite what the operator already read.
func TestObservedOutputStripsControlsKeepsLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.state.setContent("pane-1", []byte("line one\n\x1b[2Kline two\r\ttabbed\x07\n"))
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	var got decodedRunResponse
	decodeBody(t, resp, &got)
	text := got.Parts[0].Text
	if strings.ContainsAny(text, "\x1b\x07\r") {
		t.Fatalf("controls survived: %q", text)
	}
	if !strings.Contains(text, "line one\n") || !strings.Contains(text, "\ttabbed") {
		t.Fatalf("line structure and tabs must survive: %q", text)
	}
}

// ---- audit -----------------------------------------------------------------

// Run output is terminal content. The audit trail records the pane, the outcome,
// and a byte count — never a byte of the content itself.
func TestRunReadAuditRecordsNoContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const secret = "SECRET-AGENT-OUTPUT-abc123"
	h.state.setContent("pane-1", []byte("prompt> "+secret))

	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	resp.Body.Close()

	h.audit.mu.Lock()
	entries := append([]AuditEntry(nil), h.audit.entries...)
	h.audit.mu.Unlock()

	var found bool
	for _, e := range entries {
		if e.Event != "run.read" {
			continue
		}
		found = true
		if e.Resource != "pane-1" || e.Result != "ok" {
			t.Errorf("audit entry = %+v", e)
		}
		if e.Bytes != len("prompt> "+secret) {
			t.Errorf("audit bytes = %d, want %d", e.Bytes, len("prompt> "+secret))
		}
		blob, _ := json.Marshal(e)
		if strings.Contains(string(blob), secret) || strings.Contains(string(blob), "prompt>") {
			t.Fatalf("audit record leaked run output: %s", blob)
		}
	}
	if !found {
		t.Fatal("no run.read audit entry recorded")
	}
}

func TestRunReadFailureAuditRecordsNoContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.state.setReadErr(upstreamErr{upstreamNotFound})
	resp := h.authedGET(apiPrefix + "/runs/pane-1?expected_generation=7")
	resp.Body.Close()

	h.audit.mu.Lock()
	entries := append([]AuditEntry(nil), h.audit.entries...)
	h.audit.mu.Unlock()
	for _, e := range entries {
		if e.Event != "run.read" {
			continue
		}
		if e.Result != "error:"+codeRunUnavailable {
			t.Errorf("audit result = %q, want error:%s", e.Result, codeRunUnavailable)
		}
		if e.Bytes != 0 {
			t.Errorf("a failed read must record no byte count, got %d", e.Bytes)
		}
		return
	}
	t.Fatal("no run.read audit entry recorded for the failure")
}

// ---- middleware ------------------------------------------------------------

// Both run routes must be in the central table and behave like every other
// authenticated read: no session, no data.
func TestRunRoutesAreInRouteTableAndGuarded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	want := map[string]bool{apiPrefix + "/runs": false, apiPrefix + "/runs/{pane_id}": false}
	for _, rt := range h.server.Routes() {
		if _, ok := want[rt.Pattern]; !ok {
			continue
		}
		want[rt.Pattern] = true
		if !rt.RequiresAuth || !rt.RequiresLogin {
			t.Errorf("%s must require Access and a session", rt.Pattern)
		}
		if rt.Mutating || rt.WebSocket {
			t.Errorf("%s must be a non-mutating HTTP read", rt.Pattern)
		}
	}
	for pattern, present := range want {
		if !present {
			t.Errorf("route table missing %s", pattern)
		}
	}

	// Unauthenticated: rejected before the handler.
	for _, path := range []string{apiPrefix + "/runs", apiPrefix + "/runs/pane-1?expected_generation=7"} {
		resp := h.do(http.MethodGet, path, "", withOrigin(h.origin))
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without session = %d, want 401", path, resp.StatusCode)
		}
	}

	// Wrong Host: rejected by the host allowlist.
	cookie, _ := h.sessionCookie()
	resp := h.do(http.MethodGet, apiPrefix+"/runs", "", withCookie(cookie), withHost("evil.example.com"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("wrong host = %d, want 421", resp.StatusCode)
	}
}

func TestRunRoutesSetSecurityHeaders(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, path := range []string{apiPrefix + "/runs", apiPrefix + "/runs/pane-1?expected_generation=7"} {
		resp := h.authedGET(path)
		resp.Body.Close()
		for k, v := range map[string]string{
			"Cache-Control":          "no-store",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
			"X-Frame-Options":        "DENY",
		} {
			if got := resp.Header.Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", path, k, got, v)
			}
		}
		if resp.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: missing CSP", path)
		}
	}
}

// ---- unit-level bounds -----------------------------------------------------

func TestBoundObservedTextIsUTF8Safe(t *testing.T) {
	t.Parallel()
	// "é" is two bytes; cutting mid-rune must not emit an invalid sequence.
	in := strings.Repeat("é", 20)
	got, truncated := boundObservedText(in, 9)
	if !truncated {
		t.Fatal("want truncated")
	}
	if len(got) > 9 {
		t.Fatalf("len = %d, want <= 9", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(in, got) {
		t.Fatalf("truncation must keep the tail: %q", got)
	}

	if got, truncated := boundObservedText("short", 100); got != "short" || truncated {
		t.Fatalf("under-bound text = %q,%v", got, truncated)
	}
	// An unbounded configuration must still sanitize.
	if got, _ := boundObservedText("a\x1bb", 0); got != "ab" {
		t.Fatalf("unbounded sanitize = %q, want ab", got)
	}
}

func TestTruncateUTF8DoesNotSplitRunes(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("日", 10) // 3 bytes each
	got := truncateUTF8(in, 7)
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6 (two whole runes)", len(got))
	}
	if truncateUTF8("abc", 10) != "abc" {
		t.Fatal("short input must be returned unchanged")
	}
}
