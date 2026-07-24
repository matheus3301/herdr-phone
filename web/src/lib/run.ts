/**
 * Run identity and the attention inbox model.
 *
 * A "run" is the user-facing object: one live agent objective and its control
 * history. Its execution identity is a pane plus that pane's lifecycle
 * generation, so a run is *bound* to an incarnation — when Herdr recycles the
 * pane the old run is invalid rather than silently rebinding to a new occupant.
 *
 * There are two sources, and which one is live is decided by capability, never
 * by the shape of a payload:
 *
 *  - `relay`: the versioned structured run contract (`GET /api/v1/runs`,
 *    SPEC §12.1). Its `run_id` is **opaque and authoritative**; the client never
 *    parses it and addresses every operation by `pane_id` + `expected_generation`.
 *  - `snapshot`: the fallback for a relay that does not advertise the contract.
 *    Its ids are locally derived and therefore **internal only** — they are never
 *    sent to the relay, and they are never presented as a server run id.
 *
 * Nothing is invented in either mode: agent kind, name, status, cwd, and
 * Herdr's own normalized terminal title are authoritative fields, and no
 * message, tool call, approval, diff, or test result is synthesized.
 */
import { AGENT_STATUSES, type Agent, type AgentStatus, type Snapshot, type WireRunSummary } from "./types";

/* ------------------------------------------------------------- run identity */

export interface RunKey {
  paneId: string;
  generation: number;
}

/** Which authority produced a run. `relay` ids are opaque and authoritative. */
export type RunOrigin = "relay" | "snapshot";

const RUN_SEPARATOR = "~g";

/**
 * Encode a pane + generation into a single URL path segment.
 *
 * This is an **internal** identifier: the fallback's run id, and elsewhere a
 * client-side alias that lets a link built from a pane (a console, a workspace
 * pane row, a launch receipt) address a run without knowing the relay's opaque
 * id. It is never sent to the relay and never displayed as a run id.
 */
export function formatRunId(key: RunKey): string {
  return `${key.paneId}${RUN_SEPARATOR}${key.generation}`;
}

/** Decode an internal run id/alias. Null for anything malformed or non-positive. */
export function parseRunId(runId: string | undefined): RunKey | null {
  if (!runId) return null;
  const at = runId.lastIndexOf(RUN_SEPARATOR);
  if (at <= 0) return null;
  const paneId = runId.slice(0, at);
  const generation = Number(runId.slice(at + RUN_SEPARATOR.length));
  if (!paneId || !Number.isInteger(generation) || generation <= 0) return null;
  return { paneId, generation };
}

/**
 * Close the status set to Herdr's five lifecycle states, mirroring
 * internal/state/runs.go. An unrecognized value becomes `unknown`; it must
 * never be read as completion.
 */
export function normalizeRunStatus(status: string | undefined | null): AgentStatus {
  return (AGENT_STATUSES as readonly string[]).includes(status ?? "") ? (status as AgentStatus) : "unknown";
}

/* ---------------------------------------------------------------- sections */

/**
 * Inbox sections, ordered by urgency. `done` is presented as **Updated** —
 * unseen background work settled — never "Ready" or "Successful", which would
 * claim an outcome Herdr never reported. `unknown` is its own section and is
 * never folded into Idle or a completion state.
 */
export const RUN_SECTIONS = ["attention", "working", "updated", "idle", "unknown"] as const;
export type RunSection = (typeof RUN_SECTIONS)[number];

export const SECTION_TITLE: Record<RunSection, string> = {
  attention: "Needs you",
  working: "Working",
  updated: "Updated",
  idle: "Idle",
  unknown: "Status unknown",
};

const SECTION_OF: Record<AgentStatus, RunSection> = {
  blocked: "attention",
  working: "working",
  done: "updated",
  idle: "idle",
  unknown: "unknown",
};

export function sectionOf(status: AgentStatus): RunSection {
  return SECTION_OF[status];
}

/** The status word shown next to a run. Mirrors the section vocabulary. */
export function runStatusLabel(status: AgentStatus): string {
  return SECTION_TITLE[SECTION_OF[status]];
}

/** A short sentence describing what the status means, for the run header. */
export function runStatusDescription(status: AgentStatus): string {
  switch (status) {
    case "blocked":
      return "A decision is required before this run can continue.";
    case "working":
      return "The agent is running. Herdr reports no question pending.";
    case "done":
      return "Background work settled since you last looked. Herdr does not report success or failure.";
    case "idle":
      return "The agent is attached and waiting for an instruction.";
    case "unknown":
      return "Herdr cannot read this agent's state. Open the console to see what the pane is doing.";
  }
}

/* --------------------------------------------------------------- run model */

export interface Run extends RunKey {
  /**
   * The run's addressable id. Authoritative and opaque when `origin` is
   * `relay`; an internal pane+generation key when it is `snapshot`.
   */
  id: string;
  origin: RunOrigin;
  /**
   * The relay's opaque digest of the pane's occupant. It changes exactly when
   * the pane generation changes, so either one invalidates an open run. Null in
   * fallback mode, where the relay publishes no incarnation.
   */
  incarnation: string | null;
  workspaceId: string;
  tabId: string;
  agentKind: string;
  agentName: string;
  status: AgentStatus;
  section: RunSection;
  /** Herdr's own normalized pane title. Metadata, not parsed agent output. */
  terminalTitle: string;
  cwd: string;
  /** Monotonic Herdr transition counter — the only ordering signal available. */
  stateChangeSeq: number;
  interactiveReady: boolean;
  workspaceLabel: string;
  tabLabel: string;
  worktreeBranch: string | null;
  worktreePath: string | null;
}

/**
 * Map one structured-contract run onto the view model.
 *
 * Identity, generation, incarnation, status, and context all come from the
 * relay — the snapshot is consulted only for the worktree *branch*, which the
 * run contract does not carry and which is display text either way.
 */
export function runFromWire(wire: WireRunSummary, snapshot: Snapshot | null): Run {
  const status = normalizeRunStatus(wire.status);
  const workspace = snapshot?.workspaces.find((w) => w.id === wire.workspace_id);
  const agentName = wire.agent_name || wire.display_agent || wire.agent_kind;
  return {
    id: wire.run_id,
    origin: "relay",
    incarnation: wire.agent_incarnation || null,
    paneId: wire.pane_id,
    generation: wire.pane_generation,
    workspaceId: wire.workspace_id,
    tabId: wire.tab_id,
    agentKind: wire.agent_kind,
    agentName,
    status,
    section: SECTION_OF[status],
    terminalTitle: wire.title ?? "",
    cwd: wire.cwd ?? "",
    stateChangeSeq: wire.state_change_seq,
    interactiveReady: wire.interactive_ready,
    workspaceLabel: wire.workspace_label || wire.workspace_id,
    tabLabel: wire.tab_label || wire.tab_id,
    worktreeBranch: workspace?.worktree?.branch ?? null,
    worktreePath: wire.worktree?.checkout_path ?? workspace?.worktree?.path ?? null,
  };
}

/** Map a whole run list, preserving the relay's order. */
export function runsFromWire(wire: WireRunSummary[], snapshot: Snapshot | null): Run[] {
  return wire.map((run) => runFromWire(run, snapshot));
}

/**
 * Project the snapshot into runs — the fallback for a relay without the
 * structured contract. A pane whose generation is missing still produces a run
 * (generation 0) so the UI can say so plainly instead of hiding the agent;
 * every mutation path refuses to act on generation 0.
 */
export function buildRuns(snapshot: Snapshot | null): Run[] {
  if (!snapshot) return [];
  const paneById = new Map(snapshot.panes.map((p) => [p.id, p]));
  const workspaceById = new Map(snapshot.workspaces.map((w) => [w.id, w]));
  const tabById = new Map(snapshot.tabs.map((t) => [t.id, t]));

  return snapshot.agents.map((agent: Agent) => {
    const pane = paneById.get(agent.paneId);
    const workspace = workspaceById.get(agent.workspaceId);
    const tab = tabById.get(agent.tabId);
    const generation = pane?.generation ?? 0;
    return {
      id: formatRunId({ paneId: agent.paneId, generation }),
      origin: "snapshot" as const,
      incarnation: null,
      paneId: agent.paneId,
      generation,
      workspaceId: agent.workspaceId,
      tabId: agent.tabId,
      agentKind: agent.kind,
      agentName: agent.name,
      status: agent.status,
      section: SECTION_OF[agent.status],
      terminalTitle: agent.title,
      cwd: agent.cwd,
      stateChangeSeq: agent.stateChangeSeq,
      interactiveReady: agent.interactiveReady,
      workspaceLabel: workspace?.label ?? agent.workspaceId,
      tabLabel: tab?.label ?? agent.tabId,
      worktreeBranch: workspace?.worktree?.branch ?? null,
      worktreePath: workspace?.worktree?.path ?? null,
    };
  });
}

/**
 * Resolve a route parameter to a run.
 *
 * The relay's opaque `run_id` matches first and is never parsed. Only if that
 * fails is the parameter treated as an internal pane+generation alias, and even
 * then the generation must match exactly — so a link built from a pane resolves
 * to the *current* occupant's run or to nothing, never by rebinding a pane to a
 * different incarnation.
 */
export function findRun(runs: Run[], runId: string | undefined): Run | null {
  if (!runId) return null;
  const exact = runs.find((r) => r.id === runId);
  if (exact) return exact;
  const alias = parseRunId(runId);
  if (!alias) return null;
  return runs.find((r) => r.paneId === alias.paneId && r.generation === alias.generation) ?? null;
}

/**
 * The execution identity a route was opened against, used to explain an
 * invalidation once the run itself has left the list. In relay mode the id is
 * opaque, so the caller remembers the last resolved run rather than parsing it.
 */
export function runRef(runId: string | undefined, lastKnown: Run | null): RunKey | null {
  if (lastKnown) return { paneId: lastKnown.paneId, generation: lastKnown.generation };
  return parseRunId(runId);
}

/**
 * Why a run no longer resolves. Distinguishing "the pane was replaced" from
 * "the agent ended" is what lets the UI freeze the old run and offer the new
 * occupant instead of silently swapping the user's target.
 */
export type RunInvalidation =
  | { kind: "replaced"; paneId: string; successor: Run }
  | { kind: "generation-changed"; paneId: string; generation: number }
  | { kind: "gone"; paneId: string };

export function explainMissingRun(runs: Run[], snapshot: Snapshot | null, ref: RunKey | null): RunInvalidation | null {
  if (!ref) return null;
  const successor = runs.find((r) => r.paneId === ref.paneId && r.generation !== ref.generation);
  if (successor) return { kind: "replaced", paneId: ref.paneId, successor };
  const pane = snapshot?.panes.find((p) => p.id === ref.paneId);
  if (pane && pane.generation !== ref.generation) {
    return { kind: "generation-changed", paneId: ref.paneId, generation: pane.generation };
  }
  return { kind: "gone", paneId: ref.paneId };
}

/* -------------------------------------------------------------- sectioning */

export interface RunGroup {
  key: RunSection;
  title: string;
  runs: Run[];
}

/**
 * Group runs into the fixed section order. Within a section the freshest Herdr
 * transition leads; the pane id breaks ties so the order is total and stable
 * across snapshots that carry an identical sequence.
 */
export function groupRuns(runs: Run[]): RunGroup[] {
  const buckets = new Map<RunSection, Run[]>(RUN_SECTIONS.map((s) => [s, []]));
  for (const run of runs) buckets.get(run.section)!.push(run);
  for (const list of buckets.values()) {
    list.sort((a, b) => b.stateChangeSeq - a.stateChangeSeq || a.paneId.localeCompare(b.paneId));
  }
  return RUN_SECTIONS.map((key) => ({ key, title: SECTION_TITLE[key], runs: buckets.get(key)! }));
}

/** Count of runs demanding a decision — drives the nav badge and announcement. */
export function attentionCount(runs: Run[]): number {
  return runs.filter((r) => r.section === "attention").length;
}
