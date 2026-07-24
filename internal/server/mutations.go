package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

// opSpec declares an allowlisted mutation operation and its guards.
type opSpec struct {
	// requiresConfirmation marks structurally destructive operations that need a
	// single-use confirmation nonce.
	requiresConfirmation bool
	// resourceField is the params field naming the canonical target resource,
	// used for confirmation binding and (when it is "pane_id") generation checks.
	resourceField string
	// altResourceField, if set, is an alternate identifier the dispatcher would
	// prefer over resourceField. The server rejects a request that supplies it
	// with a value diverging from the canonical resource, so the guard/audit
	// always key on the exact identifier that will be acted on (M1).
	altResourceField string
}

func (o opSpec) generationChecked() bool { return o.resourceField == "pane_id" }

// operations is the complete mutation allowlist (section 15). Any operation not
// present here is rejected before any Herdr call.
var operations = map[string]opSpec{
	// Workspaces / spaces.
	"workspace.create": {resourceField: ""},
	"workspace.focus":  {resourceField: "workspace_id"},
	"workspace.rename": {resourceField: "workspace_id"},
	"workspace.close":  {resourceField: "workspace_id", requiresConfirmation: true},

	// Tabs.
	"tab.create": {resourceField: ""},
	"tab.focus":  {resourceField: "tab_id"},
	"tab.rename": {resourceField: "tab_id"},
	"tab.move":   {resourceField: "tab_id"},
	"tab.close":  {resourceField: "tab_id", requiresConfirmation: true},

	// Panes.
	"pane.focus":  {resourceField: "pane_id"},
	"pane.split":  {resourceField: "pane_id"},
	"pane.resize": {resourceField: "pane_id"},
	"pane.zoom":   {resourceField: "pane_id"},
	"pane.swap":   {resourceField: "pane_id"},
	"pane.move":   {resourceField: "pane_id"},
	"pane.rename": {resourceField: "pane_id"},
	"pane.close":  {resourceField: "pane_id", requiresConfirmation: true},

	// Agents. The dispatcher prefers "target" over "pane_id" when present, so a
	// divergent "target" is rejected to keep the generation guard effective.
	// agent.start is excluded: its dispatcher acts only on pane_id and ignores
	// "target", so rejecting a divergent target there would be gratuitous.
	"agent.focus":     {resourceField: "pane_id", altResourceField: "target"},
	"agent.prompt":    {resourceField: "pane_id", altResourceField: "target"},
	"agent.send_keys": {resourceField: "pane_id", altResourceField: "target"},
	"agent.rename":    {resourceField: "pane_id", altResourceField: "target"},
	"agent.start":     {resourceField: "pane_id"},

	// Worktrees. The dispatcher prefers "workspace_id" for removal, so a
	// divergent "workspace_id" is rejected.
	"worktree.create":       {resourceField: ""},
	"worktree.open":         {resourceField: ""},
	"worktree.remove":       {resourceField: "worktree_id", requiresConfirmation: true, altResourceField: "workspace_id"},
	"worktree.remove_force": {resourceField: "worktree_id", requiresConfirmation: true, altResourceField: "workspace_id"},
}

// confirmSpec describes a confirmable action's resource binding.
type confirmSpec struct {
	resourceField    string
	altResourceField string
}

func (c confirmSpec) generationChecked() bool { return c.resourceField == "pane_id" }

// confirmable is the set of actions that can be issued a single-use confirmation
// nonce. It is the destructive mutation operations plus terminal.takeover, which
// is not a mutator operation but still requires explicit confirmation before a
// second controller can seize a terminal (section 13).
var confirmable = map[string]confirmSpec{
	"workspace.close":       {resourceField: "workspace_id"},
	"tab.close":             {resourceField: "tab_id"},
	"pane.close":            {resourceField: "pane_id"},
	"worktree.remove":       {resourceField: "worktree_id", altResourceField: "workspace_id"},
	"worktree.remove_force": {resourceField: "worktree_id", altResourceField: "workspace_id"},
	"terminal.takeover":     {resourceField: "pane_id"},
}

// divergentAlt reports whether params supply an alternate resource identifier
// whose value diverges from the canonical resource. Such a request would be
// guarded/audited on one identifier but dispatched against another (M1).
func divergentAlt(raw json.RawMessage, altField, canonical string) bool {
	if altField == "" {
		return false
	}
	alt := paramString(raw, altField)
	return alt != "" && alt != canonical
}

// opTakeover is the confirmable action name for terminal takeover.
const opTakeover = "terminal.takeover"

// allowedOperationNames returns the sorted operation allowlist for the
// capabilities document.
func allowedOperationNames() []string {
	names := make([]string, 0, len(operations))
	for k := range operations {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ---- confirmations --------------------------------------------------------

type confirmationRequest struct {
	Operation          string          `json:"operation"`
	ResourceID         string          `json:"resource_id"`
	ExpectedGeneration uint64          `json:"expected_generation"`
	Params             json.RawMessage `json:"params"`
}

type confirmationResponse struct {
	Confirmation  string `json:"confirmation"`
	ExpiresUnixMs int64  `json:"expires_unix_ms"`
}

func (s *Server) handleConfirmations(w http.ResponseWriter, r *http.Request) {
	ident := identityFrom(r.Context())
	var req confirmationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid request body")
		return
	}
	spec, ok := confirmable[req.Operation]
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest, "operation does not require confirmation")
		return
	}
	if spec.resourceField != "" && req.ResourceID == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "missing resource id")
		return
	}
	// If params carry the resource field, it must agree with resource_id so the
	// nonce binding cannot be aimed at a different resource at mutation time.
	if spec.resourceField != "" {
		if got := paramString(req.Params, spec.resourceField); got != "" && got != req.ResourceID {
			writeError(w, http.StatusBadRequest, codeBadRequest, "resource id mismatch")
			return
		}
	}
	// Reject a divergent alternate identifier the dispatcher would prefer (M1).
	if divergentAlt(req.Params, spec.altResourceField, req.ResourceID) {
		writeError(w, http.StatusBadRequest, codeBadRequest, "conflicting resource identifiers")
		return
	}

	// For pane-scoped operations, confirm the generation is current now.
	if spec.generationChecked() {
		gen, exists := s.deps.State.Generation(req.ResourceID)
		if !exists {
			writeError(w, http.StatusNotFound, codeNotFound, "resource not found")
			return
		}
		if req.ExpectedGeneration != gen {
			writeError(w, http.StatusConflict, codeGenerationStale, "resource changed; refresh and retry")
			return
		}
	}

	norm, err := normalizeParams(req.Params)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid params")
		return
	}
	token, exp := s.nonces.issue(req.Operation, req.ResourceID, req.ExpectedGeneration, ident.SessionID, hashParams(norm), s.cfg.ConfirmationTTL)
	s.deps.Audit.Record(AuditEntry{
		Event:     "confirmation.issue",
		Subject:   ident.Subject,
		SessionID: ident.SessionID,
		Operation: req.Operation,
		Resource:  req.ResourceID,
	})
	writeJSON(w, http.StatusOK, confirmationResponse{Confirmation: token, ExpiresUnixMs: exp.UnixMilli()})
}

// ---- mutations ------------------------------------------------------------

type mutationRequest struct {
	RequestID          string          `json:"request_id"`
	Operation          string          `json:"operation"`
	DeadlineUnixMs     int64           `json:"deadline_unix_ms"`
	ExpectedGeneration uint64          `json:"expected_generation"`
	Confirmation       string          `json:"confirmation"`
	Params             json.RawMessage `json:"params"`
}

type mutationResponse struct {
	RequestID string          `json:"request_id"`
	Accepted  bool            `json:"accepted"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *errBody        `json:"error,omitempty"`
}

func (s *Server) handleMutations(w http.ResponseWriter, r *http.Request) {
	ident := identityFrom(r.Context())
	var req mutationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid request body")
		return
	}
	if req.RequestID == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "missing request id")
		return
	}
	spec, ok := operations[req.Operation]
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest, "unknown operation")
		return
	}

	// Idempotency fast path: a completed request replays its stored response
	// without re-validating (its confirmation nonce is already spent) (section 12).
	key := idemKey(ident.SessionID, req.RequestID)
	if e, ok := s.idem.get(key); ok {
		s.replay(w, e)
		return
	}

	norm, err := normalizeParams(req.Params)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid params")
		return
	}
	resource := ""
	if spec.resourceField != "" {
		resource = paramString(req.Params, spec.resourceField)
	}
	// Reject a divergent alternate identifier the dispatcher would prefer, so the
	// generation guard and audit key on the exact resource acted on (M1).
	if divergentAlt(req.Params, spec.altResourceField, resource) {
		writeError(w, http.StatusBadRequest, codeBadRequest, "conflicting resource identifiers")
		return
	}

	// For destructive operations a confirmation must be present, but the
	// single-use nonce is NOT consumed here: consumption is deferred until after
	// the idempotency reservation and the final pre-dispatch deadline check, so a
	// request that is rejected before dispatch leaves the nonce intact and can be
	// retried as-is (R2).
	if spec.requiresConfirmation && req.Confirmation == "" {
		s.writeMutationErr(w, req.RequestID, http.StatusPreconditionRequired, codeConfirmationNeeded, "confirmation required", false)
		return
	}

	// Generation guard for pane-scoped operations. The expected generation is
	// mandatory: live pane generations start at 1, so a missing/zero value can
	// never match and must be rejected rather than silently skipping the
	// fresh-state check (H1, SPEC §11).
	if spec.generationChecked() {
		if req.ExpectedGeneration == 0 {
			s.writeMutationErr(w, req.RequestID, http.StatusBadRequest, codeGenerationStale, "expected_generation is required for this operation", false)
			return
		}
		gen, exists := s.deps.State.Generation(resource)
		if !exists {
			s.writeMutationErr(w, req.RequestID, http.StatusConflict, codeGenerationStale, "resource no longer exists", true)
			return
		}
		if gen != req.ExpectedGeneration {
			s.writeMutationErr(w, req.RequestID, http.StatusConflict, codeGenerationStale, "resource changed; refresh and retry", true)
			return
		}
	}

	// Deadline: the server deadline is the shorter of its own cap and the
	// client's remaining budget minus skew, and is re-checked right before the
	// Herdr call (section 12).
	now := s.now()
	serverBudget := s.cfg.ServerMutationDeadline
	if req.DeadlineUnixMs > 0 {
		clientRemaining := time.UnixMilli(req.DeadlineUnixMs).Sub(now) - s.cfg.DeadlineSkew
		if clientRemaining <= 0 {
			s.writeMutationErr(w, req.RequestID, http.StatusGatewayTimeout, codeDeadlineExceeded, "client deadline already passed", true)
			return
		}
		if clientRemaining < serverBudget {
			serverBudget = clientRemaining
		}
	}
	if serverBudget <= 0 {
		s.writeMutationErr(w, req.RequestID, http.StatusGatewayTimeout, codeDeadlineExceeded, "no time budget", true)
		return
	}

	// Reserve the idempotency key immediately before dispatch so two concurrent
	// retries of the same request execute exactly once (H2). Validation above ran
	// without a reservation, so its early returns need no cleanup.
	entry, res := s.idem.reserve(key, s.reservationTTL())
	switch res {
	case idemDone:
		s.replay(w, entry)
		return
	case idemInFlight:
		s.writeMutationErr(w, req.RequestID, http.StatusConflict, codeConflict, "request already in progress", true)
		return
	}
	// res == idemReserved: we own execution and must complete or release the key.
	reserved := true
	defer func() {
		// Safety net: if we somehow returned without resolving the reservation,
		// release it so the key is retryable rather than wedged.
		if reserved {
			s.idem.release(key)
		}
	}()

	ctx, cancel := context.WithTimeout(r.Context(), serverBudget)
	defer cancel()

	// Final deadline check immediately before touching Herdr.
	if ctx.Err() != nil {
		s.idem.release(key)
		reserved = false
		// The nonce was not consumed, so this deadline rejection is safely
		// retryable as-is.
		s.writeMutationErr(w, req.RequestID, http.StatusGatewayTimeout, codeDeadlineExceeded, "deadline exceeded before dispatch", true)
		return
	}

	// Consume the single-use confirmation nonce now, immediately before dispatch,
	// so it is spent only when the operation is actually attempted (R2).
	if spec.requiresConfirmation {
		if !s.nonces.consume(req.Confirmation, req.Operation, resource, req.ExpectedGeneration, ident.SessionID, hashParams(norm)) {
			s.idem.release(key)
			reserved = false
			s.writeMutationErr(w, req.RequestID, http.StatusForbidden, codeConfirmationInvalid, "confirmation invalid or expired", false)
			return
		}
	}

	result, err := s.deps.Mutator.Mutate(ctx, req.Operation, req.Params)
	if err != nil {
		code, status, retryable := classifyMutateErr(ctx)
		message := "operation failed"
		if retryable && spec.requiresConfirmation {
			// The single-use nonce has already been spent on this uncertain
			// attempt, so a plain retry with the same request_id would fail on the
			// spent nonce. Do not claim the request is retryable as-is; tell the
			// client to obtain a fresh confirmation and retry (R2).
			code = codeConfirmationNeeded
			status = http.StatusPreconditionRequired
			retryable = false
			message = "operation outcome uncertain; obtain a fresh confirmation to retry"
		}
		s.deps.Audit.Record(AuditEntry{
			Event:     "mutation",
			Subject:   ident.Subject,
			SessionID: ident.SessionID,
			Operation: req.Operation,
			Resource:  resource,
			RequestID: req.RequestID,
			Result:    "error:" + code,
		})
		body, _ := json.Marshal(mutationResponse{
			RequestID: req.RequestID,
			Error:     &errBody{Code: code, Message: message, Retryable: retryable},
		})
		switch code {
		case codeConfirmationNeeded:
			// Reconfirm outcome: release the key so a deliberate, freshly
			// confirmed retry can proceed; do not cache an uncertain result.
			s.idem.release(key)
		default:
			if retryable {
				// Never cache a retryable failure: a legitimate retry must be able
				// to re-attempt once Herdr recovers (H3). Drop the reservation.
				s.idem.release(key)
			} else {
				s.idem.complete(key, status, body, s.cfg.IdempotencyTTL)
			}
		}
		reserved = false
		writeRaw(w, status, body)
		return
	}

	s.deps.Audit.Record(AuditEntry{
		Event:     "mutation",
		Subject:   ident.Subject,
		SessionID: ident.SessionID,
		Operation: req.Operation,
		Resource:  resource,
		RequestID: req.RequestID,
		Result:    "ok",
	})
	body, _ := json.Marshal(mutationResponse{
		RequestID: req.RequestID,
		Accepted:  true,
		Result:    result,
	})
	s.idem.complete(key, http.StatusOK, body, s.cfg.IdempotencyTTL)
	reserved = false
	writeRaw(w, http.StatusOK, body)
}

// replay writes a cached idempotent response.
func (s *Server) replay(w http.ResponseWriter, e idemEntry) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Idempotent-Replay", "true")
	w.WriteHeader(e.status)
	_, _ = w.Write(e.body)
}

// reservationTTL bounds how long an in-flight reservation survives if its owner
// crashes without completing or releasing it. It comfortably exceeds the maximum
// mutation runtime (the server deadline).
func (s *Server) reservationTTL() time.Duration {
	ttl := 2 * s.cfg.ServerMutationDeadline
	if ttl < 30*time.Second {
		ttl = 30 * time.Second
	}
	return ttl
}

// writeMutationErr writes a mutation error envelope. Pre-dispatch errors are not
// cached (they are deterministic and safe to re-evaluate).
func (s *Server) writeMutationErr(w http.ResponseWriter, requestID string, status int, code, msg string, retryable bool) {
	body, _ := json.Marshal(mutationResponse{
		RequestID: requestID,
		Error:     &errBody{Code: code, Message: msg, Retryable: retryable},
	})
	writeRaw(w, status, body)
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// classifyMutateErr maps a failed mutation to a code/status/retryable triple.
// The failure cause is derived from the context (deadline/cancel); any other
// mutator error is treated as an upstream Herdr fault.
func classifyMutateErr(ctx context.Context) (code string, status int, retryable bool) {
	if ctx.Err() == context.DeadlineExceeded {
		return codeDeadlineExceeded, http.StatusGatewayTimeout, true
	}
	if ctx.Err() == context.Canceled {
		return codeUnavailable, http.StatusServiceUnavailable, true
	}
	return codeInternal, http.StatusBadGateway, false
}

// normalizeParams canonicalizes params so a confirmation and its mutation hash
// identically regardless of key order or whitespace.
func normalizeParams(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	// json.Marshal emits map keys in sorted order, giving a canonical form.
	return json.Marshal(v)
}

// paramString extracts a top-level string field from params, or "" if absent.
func paramString(raw json.RawMessage, field string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}
