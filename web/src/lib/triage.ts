import type { Agent, AgentStatus, Snapshot } from "./types";

/**
 * Blocked-first triage (SPEC §14.4, §15). Blocked agents lead, working follow,
 * quiet (idle/done/unknown) collapse. Within a group, most-recent transition
 * first so the freshest attention rises.
 */

export type TriageGroupKey = "blocked" | "working" | "quiet";

export interface TriageGroup {
  key: TriageGroupKey;
  title: string;
  agents: Agent[];
}

const GROUP_OF: Record<AgentStatus, TriageGroupKey> = {
  blocked: "blocked",
  working: "working",
  idle: "quiet",
  done: "quiet",
  unknown: "quiet",
};

const RANK: Record<TriageGroupKey, number> = { blocked: 0, working: 1, quiet: 2 };

export function groupAgents(agents: Agent[]): TriageGroup[] {
  const buckets: Record<TriageGroupKey, Agent[]> = {
    blocked: [],
    working: [],
    quiet: [],
  };
  for (const a of agents) buckets[GROUP_OF[a.status]].push(a);
  // Freshest first by Herdr's monotonic state-change sequence (the backend
  // exposes no wall-clock transition time).
  for (const key of Object.keys(buckets) as TriageGroupKey[]) {
    buckets[key].sort((x, y) => y.stateChangeSeq - x.stateChangeSeq);
  }
  const titles: Record<TriageGroupKey, string> = {
    blocked: "Needs you",
    working: "Working",
    quiet: "Quiet",
  };
  return (Object.keys(buckets) as TriageGroupKey[])
    .sort((a, b) => RANK[a] - RANK[b])
    .map((key) => ({ key, title: titles[key], agents: buckets[key] }));
}

/** Count of agents currently demanding attention (drives the header badge). */
export function needsYouCount(agents: Agent[]): number {
  return agents.filter((a) => a.status === "blocked").length;
}

/** Aggregate a set of statuses into a single blocked-dominant status. */
export function aggregateStatus(statuses: AgentStatus[]): AgentStatus {
  if (statuses.includes("blocked")) return "blocked";
  if (statuses.includes("working")) return "working";
  if (statuses.includes("done")) return "done";
  if (statuses.includes("idle")) return "idle";
  return "unknown";
}

/** The pane a blocked-first "jump to attention" action should open, if any. */
export function firstBlockedPaneId(snapshot: Snapshot): string | null {
  const grouped = groupAgents(snapshot.agents);
  const blocked = grouped.find((g) => g.key === "blocked");
  return blocked && blocked.agents.length > 0 ? blocked.agents[0].paneId : null;
}
