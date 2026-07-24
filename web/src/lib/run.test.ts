import { describe, it, expect } from "vitest";
import {
  attentionCount,
  buildRuns,
  explainMissingRun,
  findRun,
  formatRunId,
  groupRuns,
  normalizeRunStatus,
  parseRunId,
  runFromWire,
  runRef,
  runStatusLabel,
  runsFromWire,
  RUN_SECTIONS,
  SECTION_TITLE,
} from "./run";
import { makeSnapshot, makeWireRun } from "@/test/fixtures";

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

describe("runs from the structured contract", () => {
  const snapshot = makeSnapshot();

  it("takes the relay's opaque run id as authoritative and never derives one", () => {
    const run = runFromWire(makeWireRun({ run_id: "opaque-handle-7" }), snapshot);
    expect(run.id).toBe("opaque-handle-7");
    expect(run.origin).toBe("relay");
    // The internal encoding is not what a relay run is addressed by.
    expect(run.id).not.toBe(formatRunId({ paneId: run.paneId, generation: run.generation }));
  });

  it("carries the pane, generation, and incarnation the relay reported", () => {
    const run = runFromWire(makeWireRun(), snapshot);
    expect(run.paneId).toBe("w1:p1");
    expect(run.generation).toBe(3);
    expect(run.incarnation).toBe("0123456789abcdef");
  });

  it("reports an unrecognized upstream status as unknown, never as completion", () => {
    expect(normalizeRunStatus("finished")).toBe("unknown");
    expect(normalizeRunStatus(undefined)).toBe("unknown");
    expect(normalizeRunStatus("done")).toBe("done");
    const run = runFromWire(makeWireRun({ status: "finished" }), snapshot);
    expect(run.status).toBe("unknown");
    expect(run.section).toBe("unknown");
  });

  it("keeps the relay's context and fills the worktree branch from the snapshot", () => {
    const run = runFromWire(makeWireRun(), snapshot);
    expect(run.workspaceLabel).toBe("space-api");
    expect(run.tabLabel).toBe("auth-refactor");
    expect(run.worktreeBranch).toBe("auth-refactor");
    expect(run.worktreePath).toBe("/Users/dev/code/space-api");
  });

  it("falls back to ids when the relay omits optional labels", () => {
    const run = runFromWire(
      makeWireRun({ workspace_label: undefined, tab_label: undefined, agent_name: undefined, display_agent: undefined }),
      null,
    );
    expect(run.workspaceLabel).toBe("w1");
    expect(run.tabLabel).toBe("w1:t1");
    expect(run.agentName).toBe("claude");
  });

  it("preserves the relay's order", () => {
    const runs = runsFromWire(
      [makeWireRun({ run_id: "b@1", pane_id: "w1:pb" }), makeWireRun({ run_id: "a@1", pane_id: "w1:pa" })],
      snapshot,
    );
    expect(runs.map((r) => r.id)).toEqual(["b@1", "a@1"]);
  });
});

describe("run invalidation", () => {
  const snapshot = makeSnapshot();
  const runs = buildRuns(snapshot);

  it("resolves only an exact generation match — it never rebinds", () => {
    expect(findRun(runs, "w1:p1~g3")?.agentName).toBe("claude");
    expect(findRun(runs, "w1:p1~g2")).toBeNull();
  });

  it("resolves a relay run by its opaque id, and by a pane alias only on an exact generation", () => {
    const relayRuns = runsFromWire([makeWireRun({ run_id: "w1:p1@3" })], snapshot);
    expect(findRun(relayRuns, "w1:p1@3")?.paneId).toBe("w1:p1");
    // A link built from a pane resolves to the current occupant…
    expect(findRun(relayRuns, "w1:p1~g3")?.id).toBe("w1:p1@3");
    // …and to nothing at all once the generation has moved on.
    expect(findRun(relayRuns, "w1:p1~g2")).toBeNull();
  });

  it("remembers the resolved run's identity rather than parsing an opaque id", () => {
    const relayRun = runsFromWire([makeWireRun({ run_id: "opaque" })], snapshot)[0];
    expect(runRef("opaque", relayRun)).toEqual({ paneId: "w1:p1", generation: 3 });
    expect(runRef("opaque", null)).toBeNull();
    expect(runRef("w1:p1~g2", null)).toEqual({ paneId: "w1:p1", generation: 2 });
  });

  it("reports a replaced pane and offers its current occupant", () => {
    const result = explainMissingRun(runs, snapshot, { paneId: "w1:p1", generation: 2 });
    expect(result).toMatchObject({ kind: "replaced", paneId: "w1:p1" });
    expect(result && "successor" in result && result.successor.id).toBe("w1:p1~g3");
  });

  it("reports a generation change when the pane survives without an agent", () => {
    const withoutAgents = { ...snapshot, agents: [] };
    expect(
      explainMissingRun(buildRuns(withoutAgents), withoutAgents, { paneId: "w1:p1", generation: 2 }),
    ).toMatchObject({ kind: "generation-changed", generation: 3 });
  });

  it("reports a vanished pane", () => {
    const gone = { ...snapshot, agents: [], panes: [] };
    expect(explainMissingRun([], gone, { paneId: "w1:p1", generation: 3 })).toMatchObject({ kind: "gone" });
  });
});
