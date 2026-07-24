/**
 * Wire → view normalization (the single mapping boundary, SPEC §16). Converts the
 * Go backend's exact snapshot/capabilities/session wire shapes into the flat view
 * models the components consume. Derivations that the backend leaves implicit:
 *   - pane.generation ← snapshot.data.generations[pane_id]
 *   - pane.zoomed / rect ← the tab's layout
 *   - tab.active ← its workspace's active_tab_id
 *   - workspace.worktree ← worktrees[] matched by open_workspace_id
 *   - agent.kind ← agent, agent.name ← name||agent, title ← terminal_title_stripped
 *   - ordering ← authoritative array order (index)
 */
import type {
  AgentStatus,
  Capabilities,
  SessionInfo,
  Snapshot,
  WireCapabilities,
  WirePairResponse,
  WireSnapshotEnvelope,
  WireTopology,
  Worktree,
} from "./types";

function status(s: AgentStatus | undefined | null): AgentStatus {
  return s ?? "unknown";
}

/** Build the flat view snapshot from the server envelope. */
export function normalizeSnapshot(env: WireSnapshotEnvelope): Snapshot | null {
  const data = env.data;
  const topo = data?.topology;
  if (!topo) return null;
  const generations = data?.generations ?? {};

  const worktreeByWorkspace = new Map<string, WireTopology["worktrees"][number]>();
  for (const wt of topo.worktrees ?? []) {
    if (wt.open_workspace_id) worktreeByWorkspace.set(wt.open_workspace_id, wt);
  }

  // Layout lookups: which pane is zoomed per tab.
  const zoomedPaneByTab = new Map<string, string | null>();
  for (const layout of topo.layouts ?? []) {
    zoomedPaneByTab.set(layout.tab_id, layout.zoomed ? layout.focused_pane_id : null);
  }

  const workspaces = (topo.workspaces ?? []).map((w) => {
    const wt = worktreeByWorkspace.get(w.workspace_id);
    return {
      id: w.workspace_id,
      number: w.number,
      label: w.label,
      focused: w.focused,
      activeTabId: w.active_tab_id,
      tabCount: w.tab_count,
      paneCount: w.pane_count,
      agentStatus: status(w.agent_status),
      ...(wt ? { worktree: { path: wt.path, branch: wt.branch ?? null } } : {}),
    };
  });

  const activeTabByWorkspace = new Map<string, string>();
  for (const w of topo.workspaces ?? []) activeTabByWorkspace.set(w.workspace_id, w.active_tab_id);

  const tabs = (topo.tabs ?? []).map((t, i) => ({
    id: t.tab_id,
    workspaceId: t.workspace_id,
    number: t.number,
    label: t.label,
    order: i,
    active: activeTabByWorkspace.get(t.workspace_id) === t.tab_id,
    focused: t.focused,
    paneCount: t.pane_count,
    agentStatus: status(t.agent_status),
  }));

  const panes = (topo.panes ?? []).map((p, i) => ({
    id: p.pane_id,
    workspaceId: p.workspace_id,
    tabId: p.tab_id,
    focused: p.focused,
    zoomed: zoomedPaneByTab.get(p.tab_id) === p.pane_id,
    cwd: p.cwd,
    title: p.title ?? p.terminal_title_stripped ?? null,
    agentKind: p.agent || null,
    agentName: p.display_agent || p.agent || null,
    agentStatus: p.agent ? status(p.agent_status) : null,
    generation: generations[p.pane_id] ?? 0,
    revision: p.revision,
    order: i,
  }));

  const agents = (topo.agents ?? []).map((a) => ({
    paneId: a.pane_id,
    workspaceId: a.workspace_id,
    tabId: a.tab_id,
    kind: a.agent,
    name: a.name || a.agent,
    title: a.terminal_title_stripped || a.terminal_title || "",
    status: status(a.agent_status),
    cwd: a.cwd,
    stateChangeSeq: a.state_change_seq,
    interactiveReady: a.interactive_ready ?? false,
  }));

  const worktrees: Worktree[] = (topo.worktrees ?? []).map((wt) => ({
    path: wt.path,
    label: wt.label,
    branch: wt.branch ?? null,
    isDetached: wt.is_detached,
    isPrunable: wt.is_prunable,
    openWorkspaceId: wt.open_workspace_id ?? null,
    removable: !!wt.open_workspace_id,
  }));

  return {
    version: env.version,
    hash: env.hash,
    herdrVersion: topo.version,
    protocol: topo.protocol,
    workspaces,
    tabs,
    panes,
    agents,
    worktrees,
    focusedWorkspaceId: topo.focused_workspace_id || null,
    focusedTabId: topo.focused_tab_id || null,
    focusedPaneId: topo.focused_pane_id || null,
  };
}

export function normalizeCapabilities(c: WireCapabilities, phoneVersion: string): Capabilities {
  const doc = c.capabilities ?? ({} as WireCapabilities["capabilities"]);
  const mode = c.status?.mode === "named" ? "named" : "quick";
  return {
    operations: c.operations ?? [],
    agentKinds: doc.agent_kinds ?? [],
    agentKindsAvailable: Array.isArray(doc.agent_kinds),
    mode,
    accessEnforced: mode === "named",
    herdrVersion: doc.herdr_version ?? c.status?.version ?? "",
    herdrProtocol: doc.herdr_protocol ?? c.status?.protocol ?? 0,
    phoneVersion,
    ready: c.status?.ready ?? false,
    clients: c.status?.clients ?? 0,
    tunnelPublicUrl: c.tunnel?.public_url ?? "",
  };
}

/**
 * Build the session view from a pair OR a GET /session response — both now carry
 * the CSRF token, expiry, and nested identity, so a cold reload recovers a fully
 * mutable session without re-pairing.
 */
export function sessionFromResponse(p: WirePairResponse): SessionInfo {
  const mode = p.identity?.mode === "named" ? "named" : "quick";
  return {
    operator: p.identity?.display || p.identity?.subject || (mode === "quick" ? "Quick Tunnel operator" : "operator"),
    mode,
    quick: !!p.identity?.quick,
    expiresUnixMs: p.expires_unix_ms,
    csrfToken: p.csrf_token,
  };
}

/** @deprecated alias retained for pairing call sites. */
export const sessionFromPair = sessionFromResponse;
