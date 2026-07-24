import { store } from "@/lib/store";
import type { AppState } from "@/lib/store";
import { runSource } from "@/lib/run-source";
import type {
  Agent,
  Capabilities,
  Pane,
  RunContract,
  SessionInfo,
  Snapshot,
  Tab,
  Workspace,
  Worktree,
  WirePairResponse,
  WireRunCapabilities,
  WireRunsResponse,
  WireRunSummary,
  WireSnapshotEnvelope,
} from "@/lib/types";

/** A view-model snapshot for component/store tests. */
export function makeSnapshot(overrides: Partial<Snapshot> = {}): Snapshot {
  const workspaces: Workspace[] = [
    { id: "w1", number: 1, label: "space-api", focused: true, activeTabId: "w1:t1", tabCount: 2, paneCount: 2, agentStatus: "blocked", worktree: { path: "/Users/dev/code/space-api", branch: "auth-refactor" } },
    { id: "w2", number: 2, label: "mobile-ui", focused: false, activeTabId: "w2:t1", tabCount: 1, paneCount: 1, agentStatus: "working" },
  ];
  const tabs: Tab[] = [
    { id: "w1:t1", workspaceId: "w1", number: 1, label: "auth-refactor", order: 0, active: true, focused: true, paneCount: 1, agentStatus: "blocked" },
    { id: "w1:t2", workspaceId: "w1", number: 2, label: "tests", order: 1, active: false, focused: false, paneCount: 1, agentStatus: "done" },
    { id: "w2:t1", workspaceId: "w2", number: 1, label: "app", order: 0, active: true, focused: false, paneCount: 1, agentStatus: "working" },
  ];
  const panes: Pane[] = [
    { id: "w1:p1", workspaceId: "w1", tabId: "w1:t1", focused: true, zoomed: false, cwd: "/Users/dev/code/space-api", title: null, agentKind: "claude", agentName: "claude", agentStatus: "blocked", generation: 3, revision: 3, order: 0 },
    { id: "w1:p2", workspaceId: "w1", tabId: "w1:t2", focused: false, zoomed: false, cwd: "/Users/dev/code/space-api", title: null, agentKind: "codex", agentName: "codex", agentStatus: "done", generation: 1, revision: 2, order: 0 },
    { id: "w2:p1", workspaceId: "w2", tabId: "w2:t1", focused: false, zoomed: false, cwd: "/Users/dev/code/mobile-ui", title: null, agentKind: "opencode", agentName: "opencode", agentStatus: "working", generation: 2, revision: 5, order: 0 },
  ];
  const agents: Agent[] = [
    { paneId: "w2:p1", workspaceId: "w2", tabId: "w2:t1", kind: "opencode", name: "opencode", title: "Refactoring mobile UI", status: "working", cwd: "/Users/dev/code/mobile-ui", stateChangeSeq: 20, interactiveReady: true },
    { paneId: "w1:p1", workspaceId: "w1", tabId: "w1:t1", kind: "claude", name: "claude", title: "Approve this command?", status: "blocked", cwd: "/Users/dev/code/space-api", stateChangeSeq: 30, interactiveReady: true },
    { paneId: "w1:p2", workspaceId: "w1", tabId: "w1:t2", kind: "codex", name: "codex", title: "api tests", status: "done", cwd: "/Users/dev/code/space-api", stateChangeSeq: 10, interactiveReady: false },
  ];
  const worktrees: Worktree[] = [
    { path: "/Users/dev/code/space-api", label: "auth-refactor", branch: "auth-refactor", isDetached: false, isPrunable: false, openWorkspaceId: "w1", removable: true },
  ];
  return {
    version: 1,
    hash: "h1",
    herdrVersion: "0.7.5",
    protocol: 17,
    workspaces,
    tabs,
    panes,
    agents,
    worktrees,
    focusedWorkspaceId: "w1",
    focusedTabId: "w1:t1",
    focusedPaneId: "w1:p1",
    ...overrides,
  };
}

export function makeSession(overrides: Partial<SessionInfo> = {}): SessionInfo {
  return { operator: "Quick Tunnel operator", mode: "quick", quick: true, expiresUnixMs: Date.now() + 3600_000, csrfToken: "csrf", ...overrides };
}

export function makePairResponse(overrides: Partial<WirePairResponse> = {}): WirePairResponse {
  return {
    csrf_token: "csrf",
    expires_unix_ms: Date.now() + 3600_000,
    identity: { subject: "", display: "Quick Tunnel operator", quick: true, mode: "quick" },
    ...overrides,
  };
}

/** GET /session response — same shape as pairing (carries CSRF + expiry). */
export function makeSessionResponse(overrides: Partial<WirePairResponse> = {}): WirePairResponse {
  return makePairResponse(overrides);
}

/**
 * The run contract as a relay that ships it advertises: authoritative about
 * identity and status, explicitly false about every semantic capability.
 */
export function makeRunContract(overrides: Partial<RunContract> = {}): RunContract {
  return {
    contractVersion: 1,
    supported: true,
    structuredMessages: false,
    structuredToolCalls: false,
    structuredInteractions: false,
    structuredDiffs: false,
    structuredTests: false,
    structuredPlans: false,
    observedTerminalOutput: true,
    partTypes: ["observed_terminal_output"],
    outputSources: ["recent", "recent-unwrapped", "visible"],
    maxOutputBytes: 65536,
    maxOutputLines: 400,
    maxRuns: 200,
    ...overrides,
  };
}

/** The same document on the wire (internal/server/runs.go runCapabilities). */
export function makeWireRunCapabilities(overrides: Partial<WireRunCapabilities> = {}): WireRunCapabilities {
  return {
    contract_version: 1,
    supported: true,
    structured_messages: false,
    structured_tool_calls: false,
    structured_interactions: false,
    structured_diffs: false,
    structured_tests: false,
    structured_plans: false,
    observed_terminal_output: true,
    part_types: ["observed_terminal_output"],
    output_sources: ["recent", "recent-unwrapped", "visible"],
    max_output_bytes: 65536,
    max_output_lines: 400,
    max_runs: 200,
    ...overrides,
  };
}

/** One structured run, matching the fields internal/server/runs.go emits. */
export function makeWireRun(overrides: Partial<WireRunSummary> = {}): WireRunSummary {
  return {
    run_id: "w1:p1@3",
    pane_id: "w1:p1",
    pane_generation: 3,
    agent_incarnation: "0123456789abcdef",
    workspace_id: "w1",
    workspace_label: "space-api",
    tab_id: "w1:t1",
    tab_label: "auth-refactor",
    terminal_id: "t1",
    agent_kind: "claude",
    agent_name: "claude",
    display_agent: "claude",
    title: "Approve this command?",
    status: "blocked",
    interactive_ready: true,
    launch_pending: false,
    focused: true,
    cwd: "/Users/dev/code/space-api",
    foreground_cwd: "/Users/dev/code/space-api",
    worktree: {
      repo_name: "space-api",
      repo_root: "/Users/dev/code/space-api",
      checkout_path: "/Users/dev/code/space-api",
      is_linked_worktree: true,
    },
    revision: 3,
    state_change_seq: 30,
    ...overrides,
  };
}

export function makeWireRunsResponse(overrides: Partial<WireRunsResponse> = {}): WireRunsResponse {
  return {
    contract_version: 1,
    capabilities: makeWireRunCapabilities(),
    snapshot_hash: "h1",
    runs: [makeWireRun()],
    truncated: false,
    ...overrides,
  };
}

/**
 * Capabilities for a relay WITHOUT the structured run contract — the fallback
 * posture. `makeCapabilities({ runs: makeRunContract() })` opts into production
 * run mode.
 */
export function makeCapabilities(overrides: Partial<Capabilities> = {}): Capabilities {
  return {
    operations: ["workspace.create", "tab.create", "pane.split", "workspace.close", "pane.close", "worktree.remove", "worktree.remove_force", "agent.prompt", "agent.start"],
    runs: null,
    agentKinds: ["claude", "codex", "opencode"],
    agentKindsAvailable: true,
    mode: "quick",
    accessEnforced: false,
    herdrVersion: "0.7.5",
    herdrProtocol: 17,
    phoneVersion: "0.1.0",
    ready: true,
    clients: 1,
    tunnelPublicUrl: "https://example.trycloudflare.com",
    ...overrides,
  };
}

/** A minimal backend snapshot envelope for normalize/api tests. */
export function makeWireEnvelope(overrides: Partial<WireSnapshotEnvelope> = {}): WireSnapshotEnvelope {
  return {
    version: 7,
    hash: "h7",
    updated_at: "2026-07-24T00:00:00Z",
    data: {
      seq: 7,
      hash: "h7",
      generations: { "w1:p1": 3, "w1:p2": 1 },
      topology: {
        version: "0.7.5",
        protocol: 17,
        focused_workspace_id: "w1",
        focused_tab_id: "w1:t1",
        focused_pane_id: "w1:p1",
        workspaces: [
          { workspace_id: "w1", number: 1, label: "space-api", focused: true, pane_count: 2, tab_count: 1, active_tab_id: "w1:t1", agent_status: "blocked" },
        ],
        tabs: [
          { tab_id: "w1:t1", workspace_id: "w1", number: 1, label: "auth-refactor", focused: true, pane_count: 2, agent_status: "blocked" },
        ],
        panes: [
          { pane_id: "w1:p1", terminal_id: "t1", workspace_id: "w1", tab_id: "w1:t1", focused: true, cwd: "/Users/dev/code/space-api", foreground_cwd: "/Users/dev/code/space-api", agent: "claude", display_agent: "claude", agent_status: "blocked", revision: 3 },
          { pane_id: "w1:p2", terminal_id: "t2", workspace_id: "w1", tab_id: "w1:t1", focused: false, cwd: "/Users/dev/code/space-api", foreground_cwd: "/Users/dev/code/space-api", title: "server", revision: 1 },
        ],
        layouts: [
          { workspace_id: "w1", tab_id: "w1:t1", zoomed: true, area: { x: 0, y: 0, width: 1, height: 1 }, focused_pane_id: "w1:p1", panes: [], splits: [] },
        ],
        agents: [
          { terminal_id: "t1", agent: "claude", name: "claude", agent_status: "blocked", workspace_id: "w1", tab_id: "w1:t1", pane_id: "w1:p1", focused: true, interactive_ready: true, state_change_seq: 30, cwd: "/Users/dev/code/space-api", foreground_cwd: "/Users/dev/code/space-api", terminal_title_stripped: "Approve this command?", revision: 3 },
        ],
        worktrees: [
          { path: "/Users/dev/code/space-api", label: "auth-refactor", branch: "auth-refactor", is_bare: false, is_detached: false, is_linked_worktree: true, is_prunable: false, open_workspace_id: "w1" },
        ],
      },
    },
    ...overrides,
  };
}

/** Patch the singleton store in place, exactly as a live update would. */
export function updateStore(patch: Partial<AppState>): void {
  (store as unknown as { set: (p: Partial<AppState>) => void }).set(patch);
}

/**
 * Seed the singleton store for a test, dropping any run list a previous test
 * left behind so run mode is decided fresh from `capabilities`. Use
 * `updateStore` for a mid-test change, which must reconcile rather than reset.
 */
export function seedStore(patch: Partial<AppState>): void {
  runSource.reset();
  updateStore(patch);
}
