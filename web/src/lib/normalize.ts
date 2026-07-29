/**
 * Wire → view normalization (the single mapping boundary, SPEC §16). Converts the
 * Go backend's exact snapshot/capabilities/session wire shapes into the flat view
 * models the components consume. Derivations that the backend leaves implicit:
 *   - pane.generation ← snapshot.data.generations[pane_id]
 *   - pane.zoomed / rect ← the tab's layout
 *   - tab.active ← its workspace's active_tab_id
 *   - workspace.worktree ← workspaces[].worktree, verbatim (SPEC §3.1: the
 *     snapshot has no top-level worktree array, and no branch anywhere)
 *   - agent.kind ← agent, agent.name ← name||agent, title ← terminal_title_stripped
 *   - ordering ← authoritative array order (index)
 */
import { relayMode } from "./relay-mode";
import type {
  AgentStatus,
  Capabilities,
  RunContract,
  SessionInfo,
  Snapshot,
  WireCapabilities,
  WirePairResponse,
  WireRunCapabilities,
  WireSnapshotEnvelope,
} from "./types";

/**
 * The one run-contract version this build implements (SPEC §12.1). The relay
 * bumps it only on a breaking change, so an unrecognized version means the
 * shape cannot be trusted and the UI must fail closed to the fallback.
 */
export const RUN_CONTRACT_VERSION = 1;

function status(s: AgentStatus | undefined | null): AgentStatus {
  return s ?? "unknown";
}

/** Build the flat view snapshot from the server envelope. */
export function normalizeSnapshot(env: WireSnapshotEnvelope): Snapshot | null {
  const data = env.data;
  const topo = data?.topology;
  if (!topo) return null;
  const generations = data?.generations ?? {};

  // Layout lookups: which pane is zoomed per tab.
  const zoomedPaneByTab = new Map<string, string | null>();
  for (const layout of topo.layouts ?? []) {
    zoomedPaneByTab.set(layout.tab_id, layout.zoomed ? layout.focused_pane_id : null);
  }

  const workspaces = (topo.workspaces ?? []).map((w) => {
    const wt = w.worktree;
    return {
      id: w.workspace_id,
      number: w.number,
      label: w.label,
      focused: w.focused,
      activeTabId: w.active_tab_id,
      tabCount: w.tab_count,
      paneCount: w.pane_count,
      agentStatus: status(w.agent_status),
      ...(wt
        ? {
            worktree: {
              repoKey: wt.repo_key,
              repoName: wt.repo_name,
              repoRoot: wt.repo_root,
              checkoutPath: wt.checkout_path,
              isLinkedWorktree: wt.is_linked_worktree,
            },
          }
        : {}),
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

  return {
    version: env.version,
    hash: env.hash,
    herdrVersion: topo.version,
    protocol: topo.protocol,
    workspaces,
    tabs,
    panes,
    agents,
    focusedWorkspaceId: topo.focused_workspace_id || null,
    focusedTabId: topo.focused_tab_id || null,
    focusedPaneId: topo.focused_pane_id || null,
  };
}

/**
 * Normalize the advertised run contract, or return null so the UI falls back.
 *
 * Null is returned for an absent document, an unimplemented contract version,
 * or `supported: false`. Nothing is inferred from the presence of a field: a
 * relay that does not say it supports structured runs does not support them.
 */
export function normalizeRunContract(runs: WireRunCapabilities | undefined | null): RunContract | null {
  if (!runs) return null;
  if (runs.contract_version !== RUN_CONTRACT_VERSION) return null;
  if (!runs.supported) return null;
  return {
    contractVersion: runs.contract_version,
    supported: true,
    structuredMessages: !!runs.structured_messages,
    structuredToolCalls: !!runs.structured_tool_calls,
    structuredInteractions: !!runs.structured_interactions,
    structuredDiffs: !!runs.structured_diffs,
    structuredTests: !!runs.structured_tests,
    structuredPlans: !!runs.structured_plans,
    observedTerminalOutput: !!runs.observed_terminal_output,
    // Absent means off. A relay that does not say it interprets output does not
    // interpret output — the same fail-closed rule as every other capability.
    heuristicInterpretation: !!runs.heuristic_interpretation,
    interpretationParsers: runs.interpretation_parsers ?? [],
    partTypes: runs.part_types ?? [],
    outputSources: runs.output_sources ?? [],
    maxOutputBytes: runs.max_output_bytes ?? 0,
    maxOutputLines: runs.max_output_lines ?? 0,
    maxRuns: runs.max_runs ?? 0,
  };
}

export function normalizeCapabilities(c: WireCapabilities, phoneVersion: string): Capabilities {
  const doc = c.capabilities ?? ({} as WireCapabilities["capabilities"]);
  const mode = relayMode(c.status?.mode);
  return {
    operations: c.operations ?? [],
    runs: normalizeRunContract(c.runs),
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
  const mode = relayMode(p.identity?.mode);
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
