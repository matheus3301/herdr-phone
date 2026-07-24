/**
 * Run identity and the attention inbox model.
 *
 * A "run" is the user-facing object: one live agent objective and its control
 * history. Its execution identity is a pane plus that pane's lifecycle
 * generation, so a run is *bound* to an incarnation — when Herdr recycles the
 * pane the old run is invalid rather than silently rebinding to a new occupant.
 *
 * There is no structured run contract on the relay yet, so everything here is
 * derived from the authoritative topology snapshot. Nothing is invented: the
 * agent kind, name, status, cwd, and Herdr's own normalized terminal title are
 * snapshot fields, and no message, tool call, or approval is synthesized.
 */
import type { Agent, AgentStatus, Snapshot } from "./types";

/* ------------------------------------------------------------- run identity */

export interface RunKey {
  paneId: string;
  generation: number;
}

const RUN_SEPARATOR = "~g";

/** Encode a pane + generation into a single URL path segment. */
export function formatRunId(key: RunKey): string {
  return `${key.paneId}${RUN_SEPARATOR}${key.generation}`;
}

/** Decode a run id. Returns null for anything malformed or non-positive. */
export function parseRunId(runId: string | undefined): RunKey | null {
  if (!runId) return null;
  const at = runId.lastIndexOf(RUN_SEPARATOR);
  if (at <= 0) return null;
  const paneId = runId.slice(0, at);
  const generation = Number(runId.slice(at + RUN_SEPARATOR.length));
  if (!paneId || !Number.isInteger(generation) || generation <= 0) return null;
  return { paneId, generation };
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
  id: string;
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
 * Project the snapshot into runs. A pane whose generation is missing still
 * produces a run (generation 0) so the UI can say so plainly instead of hiding
 * the agent; every mutation path refuses to act on generation 0.
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

/** Find a run by its encoded id. Exact generation match — no rebinding. */
export function findRun(runs: Run[], runId: string | undefined): Run | null {
  if (!runId) return null;
  return runs.find((r) => r.id === runId) ?? null;
}

/**
 * Why a run id no longer resolves. Distinguishing "the pane was replaced" from
 * "the agent ended" is what lets the UI freeze the old run and offer the new
 * occupant instead of silently swapping the user's target.
 */
export type RunInvalidation =
  | { kind: "replaced"; paneId: string; successor: Run }
  | { kind: "generation-changed"; paneId: string; generation: number }
  | { kind: "gone"; paneId: string };

export function explainMissingRun(runs: Run[], snapshot: Snapshot | null, runId: string): RunInvalidation | null {
  const key = parseRunId(runId);
  if (!key) return null;
  const successor = runs.find((r) => r.paneId === key.paneId);
  if (successor) return { kind: "replaced", paneId: key.paneId, successor };
  const pane = snapshot?.panes.find((p) => p.id === key.paneId);
  if (pane && pane.generation !== key.generation) {
    return { kind: "generation-changed", paneId: key.paneId, generation: pane.generation };
  }
  return { kind: "gone", paneId: key.paneId };
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
