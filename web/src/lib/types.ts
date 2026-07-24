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

export interface WireWorkspace {
  workspace_id: string;
  number: number;
  label: string;
  focused: boolean;
  pane_count: number;
  tab_count: number;
  active_tab_id: string;
  agent_status: AgentStatus;
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

export interface WireWorktree {
  path: string;
  label: string;
  branch?: string;
  is_bare: boolean;
  is_detached: boolean;
  is_linked_worktree: boolean;
  is_prunable: boolean;
  open_workspace_id?: string;
}

/** herdr.Snapshot — the normalized Herdr topology. */
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
  worktrees: WireWorktree[];
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
  };
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

export interface Workspace {
  id: string;
  number: number;
  label: string;
  focused: boolean;
  activeTabId: string;
  tabCount: number;
  paneCount: number;
  agentStatus: AgentStatus;
  /** Provenance when a worktree is open in this workspace. */
  worktree?: { path: string; branch: string | null };
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

export interface Worktree {
  /** Stable UI key (the backend worktree has no id). */
  path: string;
  label: string;
  branch: string | null;
  isDetached: boolean;
  isPrunable: boolean;
  /** The workspace this worktree is open in, if any. */
  openWorkspaceId: string | null;
  /** Removable only when open in a workspace (worktree.remove takes a workspace id). */
  removable: boolean;
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
  worktrees: Worktree[];
  focusedWorkspaceId: string | null;
  focusedTabId: string | null;
  focusedPaneId: string | null;
}

export interface Capabilities {
  operations: string[];
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
