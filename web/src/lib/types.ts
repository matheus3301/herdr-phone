/**
 * Relay contract (SPEC §12, §16) — reconciled to the Go backend as of the
 * contract-reconciliation pass. Two layers live here:
 *
 *  - Wire* types mirror the Go structs byte-for-byte (snake_case), so the client
 *    speaks the real backend exactly. See internal/server/{snapshot,mutations,
 *    events,pairing}.go, internal/state/snapshot.go, internal/herdr/models.go,
 *    internal/terminal/protocol.go.
 *  - View types are the flat, camelCase shapes the components consume. `normalize`
 *    (lib/normalize.ts) is the single mapping boundary from wire → view.
 */

export const AGENT_STATUSES = ["idle", "working", "blocked", "done", "unknown"] as const;
export type AgentStatus = (typeof AGENT_STATUSES)[number];

/* ============================================================ wire types === */

/**
 * `WorkspaceInfo.worktree` — the git checkout provenance Herdr reports for a
 * workspace, and the ONLY worktree context a `session.snapshot` carries (SPEC
 * §3.1; `WorkspaceWorktreeInfo` in `herdr api schema --json`, protocol 17).
 *
 * Note what is absent: there is **no branch**. A branch is only available from a
 * separate `worktree.list` call, which the relay does not make, so nothing in
 * this app may present a branch name as fact.
 */
export interface WireWorkspaceWorktree {
  repo_key: string;
  repo_name: string;
  repo_root: string;
  checkout_path: string;
  is_linked_worktree: boolean;
}

export interface WireWorkspace {
  workspace_id: string;
  number: number;
  label: string;
  focused: boolean;
  pane_count: number;
  tab_count: number;
  active_tab_id: string;
  agent_status: AgentStatus;
  /** Present when Herdr resolved the workspace to a git checkout. */
  worktree?: WireWorkspaceWorktree | null;
}

export interface WireTab {
  tab_id: string;
  workspace_id: string;
  number: number;
  label: string;
  focused: boolean;
  pane_count: number;
  agent_status: AgentStatus;
}

export interface WireAgentSession {
  source: string;
  agent: string;
  kind: string;
  value: string;
}

export interface WireScroll {
  offset_from_bottom: number;
  max_offset_from_bottom: number;
  viewport_rows: number;
}

export interface WirePane {
  pane_id: string;
  terminal_id: string;
  workspace_id: string;
  tab_id: string;
  focused: boolean;
  cwd: string;
  foreground_cwd: string;
  agent?: string;
  display_agent?: string;
  agent_session?: WireAgentSession;
  agent_status?: AgentStatus;
  label?: string;
  title?: string;
  terminal_title?: string;
  terminal_title_stripped?: string;
  scroll?: WireScroll | null;
  revision: number;
}

export interface WireAgent {
  terminal_id: string;
  agent: string;
  name?: string;
  agent_session?: WireAgentSession;
  agent_status: AgentStatus;
  workspace_id: string;
  tab_id: string;
  pane_id: string;
  focused: boolean;
  interactive_ready?: boolean;
  screen_detection_skipped?: boolean;
  state_change_seq: number;
  cwd: string;
  foreground_cwd: string;
  terminal_title?: string;
  terminal_title_stripped?: string;
  revision: number;
}

export interface WireRect {
  x: number;
  y: number;
  width: number;
  height: number;
}
export interface WireLayoutPane {
  pane_id: string;
  focused: boolean;
  rect: WireRect;
}
export interface WireLayout {
  workspace_id: string;
  tab_id: string;
  zoomed: boolean;
  area: WireRect;
  focused_pane_id: string;
  panes: WireLayoutPane[];
  splits: unknown[];
}

/**
 * herdr.Snapshot — the normalized Herdr topology.
 *
 * There is deliberately no top-level `worktrees` array: `SessionSnapshot`
 * declares none, so the relay no longer carries one either. Worktree context
 * comes from `workspaces[].worktree`.
 */
export interface WireTopology {
  version: string;
  protocol: number;
  focused_workspace_id: string;
  focused_tab_id: string;
  focused_pane_id: string;
  workspaces: WireWorkspace[];
  tabs: WireTab[];
  panes: WirePane[];
  layouts: WireLayout[];
  agents: WireAgent[];
}

/** state.Snapshot — carried inside the server envelope's `data` field. */
export interface WireStateSnapshot {
  seq: number;
  hash: string;
  topology: WireTopology | null;
  generations: Record<string, number>;
}

/** server.Snapshot — the top-level snapshot envelope (GET /snapshot, events WS). */
export interface WireSnapshotEnvelope {
  version: number;
  hash: string;
  data: WireStateSnapshot | null;
  updated_at: string;
}

export interface WireComponentHealth {
  healthy: boolean;
  detail?: string;
}
export interface WireDaemonStatus {
  version: string;
  protocol: number;
  mode: string;
  ready: boolean;
  herdr: WireComponentHealth;
  state: WireComponentHealth;
  clients: number;
}
export interface WireTunnelInfo {
  mode: string;
  public_url: string;
  health: WireComponentHealth;
}
export interface WireCapabilitiesDoc {
  herdr_version: string;
  herdr_protocol: number;
  live_handoff: boolean;
  agent_kinds?: string[];
  agent_kinds_error?: string;
}
export interface WireCapabilities {
  version: number;
  operations: string[];
  capabilities: WireCapabilitiesDoc;
  status: WireDaemonStatus;
  tunnel: WireTunnelInfo;
  limits: {
    max_body_bytes: number;
    max_pane_read_lines: number;
    confirmation_ttl_seconds: number;
    /** Present only on a relay that ships the structured run contract. */
    max_run_output_lines?: number;
    max_run_output_bytes?: number;
    max_runs?: number;
  };
  /** Absent on a relay older than the structured run contract (SPEC §12.1). */
  runs?: WireRunCapabilities;
}

/* ------------------------------------------------- structured run contract */

/**
 * SPEC §12.1, contract version 1 — mirrors internal/server/runs.go byte for
 * byte. Every semantic flag is `false` on Herdr 0.7.5: the relay is
 * authoritative about identity and status and explicit about what it cannot
 * know, and the UI must gate presentation on these flags rather than infer
 * structure the relay never advertised.
 */
export interface WireRunCapabilities {
  contract_version: number;
  supported: boolean;
  structured_messages: boolean;
  structured_tool_calls: boolean;
  structured_interactions: boolean;
  structured_diffs: boolean;
  structured_tests: boolean;
  structured_plans: boolean;
  observed_terminal_output: boolean;
  part_types: string[];
  output_sources: string[];
  max_output_bytes: number;
  max_output_lines: number;
  max_runs: number;
}

export interface WireRunWorktree {
  repo_name: string;
  repo_root: string;
  checkout_path: string;
  is_linked_worktree: boolean;
}

/** One run's authoritative identity, execution context, and status. Never output. */
export interface WireRunSummary {
  run_id: string;
  pane_id: string;
  pane_generation: number;
  agent_incarnation: string;
  workspace_id: string;
  workspace_label?: string;
  tab_id: string;
  tab_label?: string;
  terminal_id: string;
  agent_kind: string;
  agent_name?: string;
  display_agent?: string;
  title?: string;
  /** idle | working | blocked | done | unknown. Anything else reads as unknown. */
  status: string;
  interactive_ready: boolean;
  launch_pending: boolean;
  focused: boolean;
  cwd?: string;
  foreground_cwd?: string;
  worktree?: WireRunWorktree;
  revision: number;
  state_change_seq: number;
}

export interface WireRunsResponse {
  contract_version: number;
  capabilities: WireRunCapabilities;
  snapshot_hash: string;
  runs: WireRunSummary[];
  /** True when the relay's max_runs bound applied, so a short list is not complete. */
  truncated: boolean;
}

/**
 * The only part type this contract emits. It is terminal output Herdr rendered,
 * labelled as such: it carries no role and must never be presented as an
 * assistant message. A client ignores part types it does not know.
 */
export interface WireObservedOutputPart {
  type: string;
  source: string;
  format: string;
  lines: number;
  bytes: number;
  truncated: boolean;
  text: string;
}

export interface WireRunResponse {
  contract_version: number;
  capabilities: WireRunCapabilities;
  run: WireRunSummary;
  parts: WireObservedOutputPart[];
}

export interface WireIdentity {
  subject: string;
  display: string;
  quick: boolean;
  mode: string;
}
export interface WirePairResponse {
  csrf_token: string;
  expires_unix_ms: number;
  identity: WireIdentity;
}

/** GET /session now returns the same shape as pairing (csrf_token + expiry +
 * nested identity), so a cold reload recovers the CSRF token without re-pairing
 * (internal/server/pairing.go sessionResponse == pairResponse). */
export type WireSessionResponse = WirePairResponse;

export interface WireConfirmationResponse {
  confirmation: string;
  expires_unix_ms: number;
}

export interface WireMutationResponse {
  request_id: string;
  accepted?: boolean;
  result?: unknown;
  error?: { code: string; message: string; retryable?: boolean };
}

export interface WireDirectoryEntry {
  name: string;
  path: string;
}
export interface WireDirectoriesResponse {
  path: string;
  entries: WireDirectoryEntry[];
}

export interface WirePaneReadResponse {
  pane_id: string;
  source: string;
  lines: number;
  content: string;
}

/* ============================================================ view types === */

/**
 * A workspace's git checkout provenance, straight from `workspaces[].worktree`.
 *
 * `isLinkedWorktree` is what makes a removal control honest: `worktree.remove`
 * takes the *workspace* a worktree is open in and git refuses to remove a main
 * checkout, so a linked worktree is exactly the removable case. There is no
 * branch here because the snapshot carries none.
 */
export interface WorkspaceWorktree {
  repoKey: string;
  repoName: string;
  repoRoot: string;
  checkoutPath: string;
  isLinkedWorktree: boolean;
}

export interface Workspace {
  id: string;
  number: number;
  label: string;
  focused: boolean;
  activeTabId: string;
  tabCount: number;
  paneCount: number;
  agentStatus: AgentStatus;
  /** Provenance when Herdr resolved this workspace to a git checkout. */
  worktree?: WorkspaceWorktree;
}

export interface Tab {
  id: string;
  workspaceId: string;
  number: number;
  label: string;
  /** Authoritative array-order index (SPEC/Herdr: never sort by number). */
  order: number;
  active: boolean;
  focused: boolean;
  paneCount: number;
  agentStatus: AgentStatus;
}

export interface Pane {
  id: string;
  workspaceId: string;
  tabId: string;
  focused: boolean;
  zoomed: boolean;
  cwd: string;
  title: string | null;
  agentKind: string | null;
  agentName: string | null;
  agentStatus: AgentStatus | null;
  /** Lifecycle generation from the snapshot's generations map. */
  generation: number;
  revision: number;
  order: number;
}

export interface Agent {
  paneId: string;
  workspaceId: string;
  tabId: string;
  kind: string;
  name: string;
  title: string;
  status: AgentStatus;
  cwd: string;
  /**
   * Monotonic Herdr state-change sequence. The backend exposes no wall-clock
   * transition time, so freshness is ordered by this, not a timestamp.
   */
  stateChangeSeq: number;
  interactiveReady: boolean;
}

export interface Snapshot {
  /** Envelope version (state seq); advisory. */
  version: number;
  /** Top-level content hash used for change detection + ETag. */
  hash: string;
  herdrVersion: string;
  protocol: number;
  workspaces: Workspace[];
  tabs: Tab[];
  panes: Pane[];
  agents: Agent[];
  focusedWorkspaceId: string | null;
  focusedTabId: string | null;
  focusedPaneId: string | null;
}

/**
 * The structured run contract as the UI consumes it. Null when the relay does
 * not advertise it, or advertises a contract version this build does not
 * implement — either way the UI fails closed to the snapshot + `pane.read`
 * fallback rather than guessing at a shape it cannot verify.
 */
export interface RunContract {
  contractVersion: number;
  supported: boolean;
  structuredMessages: boolean;
  structuredToolCalls: boolean;
  structuredInteractions: boolean;
  structuredDiffs: boolean;
  structuredTests: boolean;
  structuredPlans: boolean;
  observedTerminalOutput: boolean;
  partTypes: string[];
  outputSources: string[];
  maxOutputBytes: number;
  maxOutputLines: number;
  maxRuns: number;
}

export interface Capabilities {
  operations: string[];
  /** Null on a relay without the versioned run contract (SPEC §12.1). */
  runs: RunContract | null;
  agentKinds: string[];
  /** False when the backend could not discover startable kinds (disables start). */
  agentKindsAvailable: boolean;
  mode: "named" | "quick";
  accessEnforced: boolean;
  herdrVersion: string;
  herdrProtocol: number;
  phoneVersion: string;
  ready: boolean;
  clients: number;
  tunnelPublicUrl: string;
}

export interface SessionInfo {
  operator: string;
  mode: "named" | "quick";
  quick: boolean;
  expiresUnixMs: number;
  /** CSRF token held in memory (SPEC §9.1: never persisted). Issued by POST /pair
   * and re-issued by GET /session, so a cold reload keeps a mutable session. */
  csrfToken: string;
}

export type ReadSource = "visible" | "recent" | "recent-unwrapped";

export interface PaneReadResult {
  paneId: string;
  source: string;
  lines: number;
  content: string;
}

export interface DirectoryEntry {
  name: string;
  path: string;
}
export interface DirectoryListing {
  path: string;
  parent: string | null;
  entries: DirectoryEntry[];
}

/* ============================================================= mutations === */

/** Allowlisted mutation operations — exact match to internal/server/mutations.go. */
export type MutationOperation =
  | "workspace.create"
  | "workspace.focus"
  | "workspace.rename"
  | "workspace.close"
  | "tab.create"
  | "tab.focus"
  | "tab.rename"
  | "tab.move"
  | "tab.close"
  | "pane.focus"
  | "pane.split"
  | "pane.resize"
  | "pane.zoom"
  | "pane.swap"
  | "pane.move"
  | "pane.rename"
  | "pane.close"
  | "agent.focus"
  | "agent.prompt"
  | "agent.send_keys"
  | "agent.rename"
  | "agent.start"
  | "worktree.create"
  | "worktree.open"
  | "worktree.remove"
  | "worktree.remove_force";

/** Confirmable actions (destructive ops + terminal.takeover). */
export type ConfirmableAction = MutationOperation | "terminal.takeover";

export interface MutationRequest {
  request_id: string;
  operation: MutationOperation;
  deadline_unix_ms: number;
  expected_generation?: number;
  confirmation?: string;
  params: Record<string, unknown>;
}

export interface MutationSuccess {
  request_id: string;
  accepted: true;
  result: unknown;
}
export interface MutationFailure {
  request_id: string;
  accepted?: false;
  error: { code: string; message: string; retryable: boolean };
}
export type MutationResponse = MutationSuccess | MutationFailure;

export interface ConfirmationRequest {
  operation: ConfirmableAction;
  resource_id: string;
  expected_generation?: number;
  params: Record<string, unknown>;
}
export interface ConfirmationResult {
  confirmation: string;
  expiresUnixMs: number;
}

/* ============================================================== realtime === */

/** Server → client on /events. Only snapshot frames are sent (no hello). */
export type EventsServerMessage = { type: "snapshot"; snapshot: WireSnapshotEnvelope };

/** Server → client control frames on /terminals (text JSON). */
export type TerminalServerControl =
  | { type: "terminal.opened"; width?: number; height?: number; full?: boolean; seq?: number }
  | { type: "terminal.conflict"; reason?: string }
  | { type: "terminal.closed"; reason?: string }
  | { type: "terminal.resized"; width?: number; height?: number }
  | { type: "terminal.pong" };

/** Client → server control frames on /terminals (text JSON). */
export type TerminalClientControl =
  | { type: "resize"; cols: number; rows: number; cell_width_px: number; cell_height_px: number }
  | { type: "scroll"; direction: "up" | "down"; lines: number; source: "wheel" | "key" }
  | { type: "release" }
  | { type: "ping" };
