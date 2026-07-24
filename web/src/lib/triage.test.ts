import { describe, it, expect } from "vitest";
import { groupAgents, needsYouCount, aggregateStatus, firstBlockedPaneId } from "./triage";
import { makeSnapshot } from "@/test/fixtures";
import type { Agent } from "./types";

const base = { title: "", cwd: "/", interactiveReady: false } as const;
const agents: Agent[] = [
  { ...base, paneId: "p1", workspaceId: "w", tabId: "t", kind: "a", name: "a", status: "working", stateChangeSeq: 10 },
  { ...base, paneId: "p2", workspaceId: "w", tabId: "t", kind: "b", name: "b", status: "blocked", stateChangeSeq: 20 },
  { ...base, paneId: "p3", workspaceId: "w", tabId: "t", kind: "c", name: "c", status: "blocked", stateChangeSeq: 40 },
  { ...base, paneId: "p4", workspaceId: "w", tabId: "t", kind: "d", name: "d", status: "idle", stateChangeSeq: 5 },
];

describe("blocked-first triage", () => {
  it("orders groups blocked, working, quiet", () => {
    const groups = groupAgents(agents);
    expect(groups.map((g) => g.key)).toEqual(["blocked", "working", "quiet"]);
  });

  it("sorts within a group by most-recent transition first", () => {
    const blocked = groupAgents(agents).find((g) => g.key === "blocked")!;
    expect(blocked.agents.map((a) => a.name)).toEqual(["c", "b"]);
  });

  it("counts agents needing you", () => {
    expect(needsYouCount(agents)).toBe(2);
  });

  it("aggregates statuses blocked-dominant", () => {
    expect(aggregateStatus(["idle", "working", "blocked"])).toBe("blocked");
    expect(aggregateStatus(["idle", "working"])).toBe("working");
    expect(aggregateStatus(["idle", "done"])).toBe("done");
    expect(aggregateStatus([])).toBe("unknown");
  });

  it("finds the first blocked pane from a snapshot", () => {
    expect(firstBlockedPaneId(makeSnapshot())).toBe("w1:p1");
  });
});
