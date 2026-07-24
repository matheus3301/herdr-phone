import { describe, it, expect } from "vitest";
import {
  attentionCount,
  buildRuns,
  explainMissingRun,
  findRun,
  formatRunId,
  groupRuns,
  parseRunId,
  runStatusLabel,
  RUN_SECTIONS,
  SECTION_TITLE,
} from "./run";
import { makeSnapshot } from "@/test/fixtures";

describe("run identity", () => {
  it("round-trips a pane id and generation", () => {
    const key = { paneId: "w1:p1", generation: 3 };
    expect(parseRunId(formatRunId(key))).toEqual(key);
  });

  it("survives a pane id containing the separator characters", () => {
    const key = { paneId: "w1:p~g2", generation: 11 };
    expect(parseRunId(formatRunId(key))).toEqual(key);
  });

  it("rejects a missing, malformed, or non-positive generation", () => {
    expect(parseRunId(undefined)).toBeNull();
    expect(parseRunId("w1:p1")).toBeNull();
    expect(parseRunId("w1:p1~g0")).toBeNull();
    expect(parseRunId("w1:p1~g-1")).toBeNull();
    expect(parseRunId("w1:p1~gabc")).toBeNull();
    expect(parseRunId("~g3")).toBeNull();
  });
});

describe("buildRuns", () => {
  it("binds each run to its pane generation and carries execution context", () => {
    const runs = buildRuns(makeSnapshot());
    const claude = runs.find((r) => r.agentName === "claude")!;
    expect(claude.paneId).toBe("w1:p1");
    expect(claude.generation).toBe(3);
    expect(claude.id).toBe("w1:p1~g3");
    expect(claude.workspaceLabel).toBe("space-api");
    expect(claude.tabLabel).toBe("auth-refactor");
    expect(claude.worktreeBranch).toBe("auth-refactor");
  });

  it("keeps an agent whose pane has no generation, marked as generation 0", () => {
    const snapshot = makeSnapshot();
    snapshot.panes = snapshot.panes.map((p) => (p.id === "w1:p1" ? { ...p, generation: 0 } : p));
    const claude = buildRuns(snapshot).find((r) => r.agentName === "claude")!;
    // Hiding it would be worse: the operator needs to see the agent and be told
    // why it cannot be driven.
    expect(claude.generation).toBe(0);
  });

  it("returns nothing for a missing snapshot", () => {
    expect(buildRuns(null)).toEqual([]);
  });
});

describe("inbox sections", () => {
  it("orders sections by urgency", () => {
    expect(RUN_SECTIONS).toEqual(["attention", "working", "updated", "idle", "unknown"]);
    expect(groupRuns(buildRuns(makeSnapshot())).map((g) => g.key)).toEqual([...RUN_SECTIONS]);
  });

  it("presents done as Updated, never as ready or successful", () => {
    expect(SECTION_TITLE.updated).toBe("Updated");
    expect(runStatusLabel("done")).toBe("Updated");
    expect(runStatusLabel("done")).not.toMatch(/ready|success|complete|done/i);
  });

  it("keeps unknown separate from idle and from any completion state", () => {
    expect(SECTION_TITLE.unknown).toBe("Status unknown");
    const snapshot = makeSnapshot();
    snapshot.agents = snapshot.agents.map((a) => (a.name === "codex" ? { ...a, status: "unknown" as const } : a));
    const groups = groupRuns(buildRuns(snapshot));
    const unknown = groups.find((g) => g.key === "unknown")!;
    const idle = groups.find((g) => g.key === "idle")!;
    const updated = groups.find((g) => g.key === "updated")!;
    expect(unknown.runs.map((r) => r.agentName)).toEqual(["codex"]);
    expect(idle.runs).toEqual([]);
    expect(updated.runs).toEqual([]);
  });

  it("sorts the freshest Herdr transition first, with a total tiebreak", () => {
    const snapshot = makeSnapshot();
    snapshot.agents = [
      { ...snapshot.agents[0], name: "b", paneId: "w1:pb", status: "working", stateChangeSeq: 5 },
      { ...snapshot.agents[0], name: "a", paneId: "w1:pa", status: "working", stateChangeSeq: 5 },
      { ...snapshot.agents[0], name: "c", paneId: "w1:pc", status: "working", stateChangeSeq: 9 },
    ];
    const working = groupRuns(buildRuns(snapshot)).find((g) => g.key === "working")!;
    expect(working.runs.map((r) => r.agentName)).toEqual(["c", "a", "b"]);
  });

  it("counts only runs demanding a decision", () => {
    expect(attentionCount(buildRuns(makeSnapshot()))).toBe(1);
  });
});

describe("run invalidation", () => {
  const snapshot = makeSnapshot();
  const runs = buildRuns(snapshot);

  it("resolves only an exact generation match — it never rebinds", () => {
    expect(findRun(runs, "w1:p1~g3")?.agentName).toBe("claude");
    expect(findRun(runs, "w1:p1~g2")).toBeNull();
  });

  it("reports a replaced pane and offers its current occupant", () => {
    const result = explainMissingRun(runs, snapshot, "w1:p1~g2");
    expect(result).toMatchObject({ kind: "replaced", paneId: "w1:p1" });
    expect(result && "successor" in result && result.successor.id).toBe("w1:p1~g3");
  });

  it("reports a generation change when the pane survives without an agent", () => {
    const withoutAgents = { ...snapshot, agents: [] };
    expect(explainMissingRun(buildRuns(withoutAgents), withoutAgents, "w1:p1~g2")).toMatchObject({
      kind: "generation-changed",
      generation: 3,
    });
  });

  it("reports a vanished pane", () => {
    const gone = { ...snapshot, agents: [], panes: [] };
    expect(explainMissingRun([], gone, "w1:p1~g3")).toMatchObject({ kind: "gone" });
  });
});
