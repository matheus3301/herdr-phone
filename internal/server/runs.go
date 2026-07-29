package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/matheus3301/herdr-phone/internal/interpret"
	"github.com/matheus3301/herdr-phone/internal/security"
)

// The structured run contract (SPEC §12.1).
//
// Herdr v0.7.5 (protocol 17) exposes no conversation surface: its 89 socket
// methods contain no message, transcript, role, tool-call, interaction,
// approval, diff, or test-result data. The only agent-observable signals are the
// five lifecycle states, per-pane identity/context, and rendered terminal text
// from `pane.read`/`agent.read`.
//
// This contract therefore ships exactly what is authoritative — run identity,
// pane generation, agent incarnation, topology context, status — plus one
// explicitly typed part carrying bounded *observed terminal output*. It
// advertises the absence of every semantic capability so the browser fails
// closed to terminal-output presentation instead of styling bytes as assistant
// messages. Nothing here infers a message role, an approval, a diff, a test
// result, or a tool call, and no agent-specific transcript file is parsed.

// runContractVersion is the version of the run wire contract. It is bumped only
// on a breaking change; additive fields and additive part types do not bump it,
// so a client must ignore unknown fields and unknown part types.
const runContractVersion = 1

// partObservedTerminalOutput is the part type this build always emits. It is
// terminal output that Herdr rendered, labelled as such. It carries no role and
// must never be presented as an assistant message.
const partObservedTerminalOutput = "observed_terminal_output"

// The experimental interpreted part types (SPEC §12.2). They are emitted only when
// `[experimental] agent_output_parsing` is on and a parser recognized the pane, and
// they are purely additive: partObservedTerminalOutput is still emitted alongside
// them, so the raw tail and the console never stop being available.
const (
	partInterpretedTranscript  = "interpreted_transcript"
	partInterpretedInteraction = "interpreted_interaction"
)

// defaultRunOutputLines is the observed-output line count when the client does
// not ask for one. It is clamped to Config.MaxRunOutputLines.
const defaultRunOutputLines = 200

// maxRunTextFieldLen bounds every single-line display string on the wire so a
// hostile label or title cannot bloat a response.
const maxRunTextFieldLen = 512

// RunProjection is the run-scoped view of exactly one snapshot: its content hash
// and the runs projected from it. They travel together so a client can correlate
// a run list with the topology snapshot it came from, and so the server never has
// to re-serialize a snapshot just to learn its hash.
type RunProjection struct {
	SnapshotHash string
	Runs         []RunSummary
}

// RunWorktree is a run's git checkout provenance.
type RunWorktree struct {
	RepoName         string `json:"repo_name"`
	RepoRoot         string `json:"repo_root"`
	CheckoutPath     string `json:"checkout_path"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

// RunSummary is the authoritative identity, execution context, and status of one
// agent run. It never contains output, transcript, or terminal content, so it is
// safe to return in a list and safe to poll.
type RunSummary struct {
	// RunID is opaque. Operations are addressed by PaneID plus PaneGeneration,
	// never by parsing this value.
	RunID string `json:"run_id"`
	// PaneID is the canonical identifier every run-scoped request must send.
	PaneID string `json:"pane_id"`
	// PaneGeneration is the pane's lifecycle generation. A client must echo it as
	// expected_generation; a mismatch invalidates the run.
	PaneGeneration uint64 `json:"pane_generation"`
	// AgentIncarnation is an opaque digest of the pane's current occupant. It
	// changes exactly when PaneGeneration changes.
	AgentIncarnation string `json:"agent_incarnation"`

	WorkspaceID    string `json:"workspace_id"`
	WorkspaceLabel string `json:"workspace_label,omitempty"`
	TabID          string `json:"tab_id"`
	TabLabel       string `json:"tab_label,omitempty"`
	TerminalID     string `json:"terminal_id"`

	AgentKind    string `json:"agent_kind"`
	AgentName    string `json:"agent_name,omitempty"`
	DisplayAgent string `json:"display_agent,omitempty"`
	Title        string `json:"title,omitempty"`

	// Status is exactly one of idle, working, blocked, done, unknown. An
	// upstream value outside that set is reported as unknown.
	Status string `json:"status"`

	InteractiveReady bool `json:"interactive_ready"`
	LaunchPending    bool `json:"launch_pending"`
	Focused          bool `json:"focused"`

	CWD           string       `json:"cwd,omitempty"`
	ForegroundCWD string       `json:"foreground_cwd,omitempty"`
	Worktree      *RunWorktree `json:"worktree,omitempty"`

	Revision       int64 `json:"revision"`
	StateChangeSeq int64 `json:"state_change_seq"`
}

// runCapabilities advertises exactly what this build can supply. Every semantic
// capability is reported false because the pinned Herdr build has no structured
// source for it; a client must gate its presentation on these flags and fall
// back to observed terminal output rather than inferring structure.
type runCapabilities struct {
	ContractVersion int  `json:"contract_version"`
	Supported       bool `json:"supported"`

	StructuredMessages     bool `json:"structured_messages"`
	StructuredToolCalls    bool `json:"structured_tool_calls"`
	StructuredInteractions bool `json:"structured_interactions"`
	StructuredDiffs        bool `json:"structured_diffs"`
	StructuredTests        bool `json:"structured_tests"`
	StructuredPlans        bool `json:"structured_plans"`
	ObservedTerminalOutput bool `json:"observed_terminal_output"`

	// HeuristicInterpretation reports the experimental parsing feature (SPEC
	// §12.2). It is deliberately NOT one of the StructuredMessages family: those
	// mean "the relay has authoritative semantic data", and this means "the relay
	// guessed by pattern-matching a third-party TUI". A client gates the chat
	// presentation on this flag and must never treat it as a fidelity upgrade.
	HeuristicInterpretation bool     `json:"heuristic_interpretation"`
	InterpretationParsers   []string `json:"interpretation_parsers,omitempty"`

	PartTypes     []string `json:"part_types"`
	OutputSources []string `json:"output_sources"`

	MaxOutputBytes int `json:"max_output_bytes"`
	MaxOutputLines int `json:"max_output_lines"`
	MaxRuns        int `json:"max_runs"`
}

// runCapabilities returns the advertised run capabilities for this build.
func (s *Server) runCapabilities() runCapabilities {
	return runCapabilities{
		ContractVersion:        runContractVersion,
		Supported:              true,
		StructuredMessages:     false,
		StructuredToolCalls:    false,
		StructuredInteractions: false,
		StructuredDiffs:        false,
		StructuredTests:        false,
		StructuredPlans:        false,
		ObservedTerminalOutput: true,
		PartTypes:              s.runPartTypes(),
		OutputSources:          runOutputSources(),
		MaxOutputBytes:         s.cfg.MaxRunOutputBytes,
		MaxOutputLines:         s.cfg.MaxRunOutputLines,
		MaxRuns:                s.cfg.MaxRuns,

		HeuristicInterpretation: s.cfg.Interpretation.Enabled,
		InterpretationParsers:   s.interpretationParsers(),
	}
}

// runPartTypes advertises the part types this build can emit. The interpreted
// types appear only while the experimental flag is on, so a client that gates on
// the advertised list sees exactly today's contract when it is off.
func (s *Server) runPartTypes() []string {
	types := []string{partObservedTerminalOutput}
	if s.cfg.Interpretation.Enabled {
		types = append(types, partInterpretedTranscript, partInterpretedInteraction)
	}
	return types
}

// interpretationParsers returns the enabled parser list, or nil when the feature
// is off so the field is omitted entirely rather than published as an empty array.
func (s *Server) interpretationParsers() []string {
	if !s.cfg.Interpretation.Enabled {
		return nil
	}
	out := append([]string(nil), s.cfg.Interpretation.Parsers...)
	sort.Strings(out)
	return out
}

// runOutputSources is the sorted, allowlisted set of observed-output sources. It
// is the same set the pane read route accepts; `detection` is deliberately
// excluded because it is Herdr's classifier buffer, not operator-facing output.
func runOutputSources() []string {
	return []string{"recent", "recent-unwrapped", "visible"}
}

// runsResponse is the bounded run inbox. It deliberately contains no output: a
// transcript body must never ride along with a topology-shaped projection.
type runsResponse struct {
	ContractVersion int             `json:"contract_version"`
	Capabilities    runCapabilities `json:"capabilities"`
	SnapshotHash    string          `json:"snapshot_hash"`
	Runs            []RunSummary    `json:"runs"`
	// Truncated reports that MaxRuns bounded the list, so the client knows the
	// cap applied rather than reading a short list as complete.
	Truncated bool `json:"truncated"`
}

// observedOutputPart is the one typed part this build emits.
type observedOutputPart struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Format string `json:"format"`
	// Lines is how many lines Text actually contains — not the bound that was
	// requested. A client renders this as a statement of fact ("the last N lines
	// this pane rendered"), so echoing the request back would make it a lie
	// whenever the pane rendered fewer lines than were asked for.
	Lines     int    `json:"lines"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
	Text      string `json:"text"`
}

// interpretedTurn is one turn of the experimental transcript.
type interpretedTurn struct {
	// Kind is agent_text, tool_call, tool_result, or status. A client must ignore
	// an unknown kind rather than guess at it.
	Kind string `json:"kind"`
	Tool string `json:"tool,omitempty"`
	Text string `json:"text"`
}

// interpretedTranscriptPart is the chat-shaped reading of the pane.
type interpretedTranscriptPart struct {
	Type   string `json:"type"`
	Parser string `json:"parser"`
	// Experimental is always true. It is on the wire so a single response is
	// self-describing and a client cannot mistake this for authoritative data.
	Experimental bool              `json:"experimental"`
	Turns        []interpretedTurn `json:"turns"`
	DroppedTurns int               `json:"dropped_turns"`
	DroppedLines int               `json:"dropped_lines"`
	// StartsMidTurn reports that the first turn began above the top of the bounded
	// read, so it is a tail rather than a whole turn. Common on a busy pane, and the
	// UI must say so instead of presenting a fragment as a complete answer.
	StartsMidTurn bool `json:"starts_mid_turn,omitempty"`
}

// interpretedOption is one choice an interaction appears to offer.
//
// SendKey is empty whenever the option cannot be answered remotely, which is
// always the case for a selection-row prompt whose highlight is invisible in text
// mode. The relay synthesizes it from the parsed ordinal; it is never lifted from
// screen text (SPEC §12.2).
type interpretedOption struct {
	Label   string `json:"label"`
	SendKey string `json:"send_key,omitempty"`
}

type interpretedDiffLine struct {
	Line int    `json:"line,omitempty"`
	Op   string `json:"op"`
	Text string `json:"text"`
}

// interpretedInteractionPart is the one prompt the pane appears to be blocked on.
type interpretedInteractionPart struct {
	Type         string `json:"type"`
	Parser       string `json:"parser"`
	Experimental bool   `json:"experimental"`
	// Interaction is approval or question.
	Interaction string   `json:"interaction"`
	Title       string   `json:"title,omitempty"`
	Detail      []string `json:"detail,omitempty"`
	Question    string   `json:"question,omitempty"`
	// Answerable is true only when every option carries a send key. A client must
	// gate its action affordances on this and not on len(options).
	Answerable bool                  `json:"answerable"`
	Options    []interpretedOption   `json:"options,omitempty"`
	Diff       []interpretedDiffLine `json:"diff,omitempty"`
}

// runResponse is one run plus its bounded parts.
//
// Parts is []any because the contract is a heterogeneous typed-part list: the
// observed-output part is always present and the interpreted parts are appended
// only when the experimental feature is on and a parser matched. Each element
// carries its own `type`, which is how a client dispatches.
type runResponse struct {
	ContractVersion int             `json:"contract_version"`
	Capabilities    runCapabilities `json:"capabilities"`
	Run             RunSummary      `json:"run"`
	// Parts is ordered oldest-to-newest and may be empty. A client must ignore
	// part types it does not know and must never render a part as an assistant
	// message unless its type says so.
	Parts []any `json:"parts"`
}

// handleRuns returns the bounded run inbox derived from the current snapshot. It
// performs no Herdr call.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	// Run identity and status are high-frequency and must never be cached, not
	// even revalidated: no-store is already set by the central middleware and is
	// asserted here so the route's posture is explicit at the handler.
	w.Header().Set("Cache-Control", "no-store")

	projection := s.deps.State.Runs()
	runs, truncated := s.boundedRuns(projection)
	resp := runsResponse{
		ContractVersion: runContractVersion,
		Capabilities:    s.runCapabilities(),
		SnapshotHash:    projection.SnapshotHash,
		Runs:            runs,
		Truncated:       truncated,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "runs encode failed")
		return
	}
	s.writeMaybeGzip(w, r, http.StatusOK, "application/json; charset=utf-8", body)
}

// boundedRuns sanitizes and bounds a projection's run list.
func (s *Server) boundedRuns(projection RunProjection) (runs []RunSummary, truncated bool) {
	projected := projection.Runs
	if len(projected) > s.cfg.MaxRuns {
		projected = projected[:s.cfg.MaxRuns]
		truncated = true
	}
	out := make([]RunSummary, 0, len(projected))
	for _, run := range projected {
		out = append(out, sanitizeRun(run))
	}
	return out, truncated
}

// handleRun returns one run plus bounded observed output. It is guarded by the
// canonical pane id and a mandatory expected_generation, exactly like a
// pane-scoped mutation, so a client can never read through a recycled pane.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	ident := identityFrom(r.Context())
	w.Header().Set("Cache-Control", "no-store")

	paneID := r.PathValue("pane_id")
	if paneID == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "missing pane id")
		return
	}

	q := r.URL.Query()

	// The expected generation is mandatory: live generations start at 1, so a
	// missing or zero value can never match and must be rejected rather than
	// silently skipping the fresh-state check (SPEC §11).
	raw := q.Get("expected_generation")
	if raw == "" {
		writeError(w, http.StatusBadRequest, codeGenerationStale, "expected_generation is required to read a run")
		return
	}
	expectedGen, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || expectedGen == 0 {
		writeError(w, http.StatusBadRequest, codeGenerationStale, "expected_generation is required to read a run")
		return
	}

	source := q.Get("source")
	if source == "" {
		source = "recent-unwrapped"
	}
	if !paneReadSources[source] {
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid source")
		return
	}
	lines := defaultRunOutputLines
	if v := q.Get("lines"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, codeBadRequest, "invalid lines")
			return
		}
		lines = n
	}
	if lines > s.cfg.MaxRunOutputLines {
		lines = s.cfg.MaxRunOutputLines
	}

	// Generation guard before any upstream read, so a stale request never causes
	// a Herdr call at all.
	gen, exists := s.deps.State.Generation(paneID)
	if !exists {
		writeError(w, http.StatusConflict, codeGenerationStale, "pane no longer exists")
		return
	}
	if gen != expectedGen {
		writeError(w, http.StatusConflict, codeGenerationStale, "pane changed; refresh and retry")
		return
	}

	run, ok := s.findRun(paneID)
	if !ok {
		writeError(w, http.StatusNotFound, codeRunUnavailable, "no agent run occupies this pane")
		return
	}
	// The projection and the guard read the same generation map, but they are two
	// reads; if the pane was replaced between them, fail closed rather than
	// return a run bound to a generation the client did not assert.
	if run.PaneGeneration != expectedGen {
		writeError(w, http.StatusConflict, codeGenerationStale, "pane changed; refresh and retry")
		return
	}

	content, err := s.deps.State.ReadPane(r.Context(), paneID, source, lines)
	if err != nil {
		code, status := classifyReadErr(r.Context(), err)
		// Audit the failure by identity and code only. No content exists to leak
		// here, and none is recorded even on success.
		s.deps.Audit.Record(AuditEntry{
			Event:     "run.read",
			Subject:   ident.Subject,
			SessionID: ident.SessionID,
			Resource:  paneID,
			Result:    "error:" + code,
		})
		writeError(w, status, code, "run output unavailable")
		return
	}

	text, truncatedOutput := boundObservedText(string(content), s.cfg.MaxRunOutputBytes)
	// The observed-output part is emitted unconditionally and identically whether
	// or not interpretation is enabled. Interpretation is additive by construction:
	// the raw tail is the fallback the UI degrades to, so it must never be replaced.
	parts := []any{observedOutputPart{
		Type:      partObservedTerminalOutput,
		Source:    source,
		Format:    "text",
		Lines:     countLines(text),
		Bytes:     len(text),
		Truncated: truncatedOutput,
		Text:      text,
	}}
	parts = append(parts, s.interpretedParts(run.AgentKind, text)...)

	resp := runResponse{
		ContractVersion: runContractVersion,
		Capabilities:    s.runCapabilities(),
		Run:             run,
		Parts:           parts,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "run encode failed")
		return
	}
	// Audit records the byte count and outcome only: run output, like terminal
	// content, is never logged or persisted (SPEC §17, SECURITY.md).
	s.deps.Audit.Record(AuditEntry{
		Event:     "run.read",
		Subject:   ident.Subject,
		SessionID: ident.SessionID,
		Resource:  paneID,
		Result:    "ok",
		Bytes:     len(text),
	})
	s.writeMaybeGzip(w, r, http.StatusOK, "application/json; charset=utf-8", body)
}

// interpretedParts runs the experimental heuristic pass and returns the parts it
// produced, or nil.
//
// nil is returned — and no parser code runs at all — when the feature is off, when
// the pane's agent kind is not in the configured parser list, or when the parser
// found nothing it recognized. A no-match is a normal outcome, not an error: the
// client falls back to the observed-output part that was already emitted.
//
// The input is the *already sanitized and byte-bounded* observed text, so the
// parser inherits the boundary guarantees rather than re-establishing them, and
// nothing here is logged or persisted.
func (s *Server) interpretedParts(agentKind, text string) []any {
	if !s.cfg.Interpretation.parses(agentKind) {
		return nil
	}
	lim := interpret.DefaultLimits()
	lim.MaxTurns = s.cfg.Interpretation.MaxTurns

	res, ok := interpret.Parse(interpret.Kind(agentKind), text, lim)
	if !ok {
		return nil
	}

	var parts []any
	if len(res.Turns) > 0 {
		turns := make([]interpretedTurn, 0, len(res.Turns))
		for _, t := range res.Turns {
			turns = append(turns, interpretedTurn{
				Kind: string(t.Kind),
				Tool: t.Tool,
				Text: t.Text,
			})
		}
		parts = append(parts, interpretedTranscriptPart{
			Type:          partInterpretedTranscript,
			Parser:        string(res.Parser),
			Experimental:  true,
			Turns:         turns,
			DroppedTurns:  res.DroppedTurns,
			DroppedLines:  res.DroppedLines,
			StartsMidTurn: res.PartialLead,
		})
	}

	if in := res.Interaction; in != nil {
		opts := make([]interpretedOption, 0, len(in.Options))
		for _, o := range in.Options {
			opts = append(opts, interpretedOption{Label: o.Label, SendKey: o.SendKey})
		}
		diff := make([]interpretedDiffLine, 0, len(in.Diff))
		for _, d := range in.Diff {
			diff = append(diff, interpretedDiffLine{Line: d.Line, Op: string(d.Op), Text: d.Text})
		}
		parts = append(parts, interpretedInteractionPart{
			Type:         partInterpretedInteraction,
			Parser:       string(res.Parser),
			Experimental: true,
			Interaction:  string(in.Kind),
			Title:        in.Title,
			Detail:       in.Detail,
			Question:     in.Question,
			Answerable:   in.Answerable,
			Options:      opts,
			Diff:         diff,
		})
	}
	return parts
}

// findRun locates one sanitized run by canonical pane id.
func (s *Server) findRun(paneID string) (RunSummary, bool) {
	for _, run := range s.deps.State.Runs().Runs {
		if run.PaneID == paneID {
			return sanitizeRun(run), true
		}
	}
	return RunSummary{}, false
}

// sanitizeRun bounds and strips every display string on a run. Labels, titles,
// and paths originate upstream and can carry control characters; folding them to
// a single safe line keeps them from steering a terminal, a log sink, or a
// renderer.
func sanitizeRun(run RunSummary) RunSummary {
	run.WorkspaceLabel = sanitizeRunField(run.WorkspaceLabel)
	run.TabLabel = sanitizeRunField(run.TabLabel)
	run.AgentName = sanitizeRunField(run.AgentName)
	run.DisplayAgent = sanitizeRunField(run.DisplayAgent)
	run.Title = sanitizeRunField(run.Title)
	run.CWD = sanitizeRunField(run.CWD)
	run.ForegroundCWD = sanitizeRunField(run.ForegroundCWD)
	if run.Worktree != nil {
		wt := *run.Worktree
		wt.RepoName = sanitizeRunField(wt.RepoName)
		wt.RepoRoot = sanitizeRunField(wt.RepoRoot)
		wt.CheckoutPath = sanitizeRunField(wt.CheckoutPath)
		run.Worktree = &wt
	}
	return run
}

func sanitizeRunField(s string) string {
	if s == "" {
		return s
	}
	out := security.SanitizeLogLine(s)
	if len(out) > maxRunTextFieldLen {
		out = truncateUTF8(out, maxRunTextFieldLen)
	}
	return out
}

// boundObservedText sanitizes and byte-bounds observed terminal output. When the
// text exceeds the bound the *tail* is kept, because the most recent output is
// what an operator needs, and truncation is reported explicitly.
func boundObservedText(s string, maxBytes int) (text string, truncated bool) {
	s = security.SanitizeTextBlock(s)
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	tail := s[len(s)-maxBytes:]
	// Advance past a partial leading rune so the result is always valid UTF-8.
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return tail, true
}

// countLines reports how many lines s contains. Empty text is zero lines; a
// trailing newline terminates the last line rather than starting a new one.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// truncateUTF8 cuts s to at most maxBytes without splitting a rune.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// classifyReadErr maps a failed observed-output read to a stable code/status
// pair. Distinct upstream failures stay distinct — a vanished pane, a timeout, a
// dead socket, and an unclassified fault are four different client decisions —
// but the upstream message is never forwarded, because a Herdr error message can
// quote pane content.
func classifyReadErr(ctx context.Context, err error) (code string, status int) {
	if ctx.Err() == context.DeadlineExceeded {
		return codeDeadlineExceeded, http.StatusGatewayTimeout
	}
	if ctx.Err() == context.Canceled {
		return codeUnavailable, http.StatusServiceUnavailable
	}
	switch upstreamCode(err) {
	case upstreamNotFound:
		return codeRunUnavailable, http.StatusNotFound
	case upstreamInvalidParams, upstreamInvalidRequest:
		return codeBadRequest, http.StatusBadRequest
	case upstreamFeatureDisabled, upstreamPlatformUnsupported, upstreamPluginDisabled, upstreamIncompatible:
		return codeUnsupported, http.StatusNotImplemented
	case upstreamTimeout:
		return codeDeadlineExceeded, http.StatusGatewayTimeout
	case upstreamConnect, upstreamTransport, upstreamCanceled:
		return codeUnavailable, http.StatusServiceUnavailable
	default:
		return codeRunReadFailed, http.StatusBadGateway
	}
}
