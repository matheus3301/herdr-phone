import { describe, it, expect } from "vitest";
import { checkPaneTarget, FORBIDDEN_PANE_ALIASES, isPaneScoped, paneParams, PANE_SCOPED_OPERATIONS } from "./pane-ops";

describe("pane-scoped operation set", () => {
  it("matches the relay's generation-guarded allowlist", () => {
    // internal/server/mutations.go guards every operation whose resourceField is
    // "pane_id". Drift here means the client sends requests the relay refuses.
    expect([...PANE_SCOPED_OPERATIONS].sort()).toEqual(
      [
        "agent.focus",
        "agent.prompt",
        "agent.rename",
        "agent.send_keys",
        "agent.start",
        "pane.close",
        "pane.focus",
        "pane.move",
        "pane.rename",
        "pane.resize",
        "pane.split",
        "pane.swap",
        "pane.zoom",
      ].sort(),
    );
  });

  it("does not treat workspace, tab, or worktree operations as pane-scoped", () => {
    for (const op of ["workspace.create", "workspace.close", "tab.move", "worktree.remove"]) {
      expect(isPaneScoped(op)).toBe(false);
    }
  });
});

describe("checkPaneTarget", () => {
  it("accepts a live pane with a positive generation", () => {
    expect(checkPaneTarget({ paneId: "w1:p1", generation: 3 })).toBeNull();
  });

  it("refuses a zero generation — live generations start at 1", () => {
    const problem = checkPaneTarget({ paneId: "w1:p1", generation: 0 });
    expect(problem?.code).toBe("generation_missing");
    expect(problem?.message).toMatch(/generation is unknown/i);
  });

  it("refuses a fractional, negative, or absent generation", () => {
    expect(checkPaneTarget({ paneId: "w1:p1", generation: -2 })).not.toBeNull();
    expect(checkPaneTarget({ paneId: "w1:p1", generation: 1.5 })).not.toBeNull();
    expect(checkPaneTarget(null)).not.toBeNull();
    expect(checkPaneTarget({ paneId: "", generation: 3 })).not.toBeNull();
  });
});

describe("paneParams", () => {
  it("always sends the canonical pane_id", () => {
    expect(paneParams({ paneId: "w1:p1", generation: 3 }, { text: "go" })).toEqual({
      pane_id: "w1:p1",
      text: "go",
    });
  });

  it("strips every dispatcher-preferred alias", () => {
    // A divergent `target` would make the relay guard one pane and dispatch
    // against another, so the client never sends one at all.
    const params = paneParams({ paneId: "w1:p1", generation: 3 }, {
      target: "claude",
      agent: "claude",
      name_target: "claude",
      name: "claude-2",
    });
    expect(params).toEqual({ pane_id: "w1:p1", name: "claude-2" });
    for (const alias of FORBIDDEN_PANE_ALIASES) expect(params).not.toHaveProperty(alias);
  });

  it("cannot be talked out of its own pane id", () => {
    expect(paneParams({ paneId: "w1:p1", generation: 3 }, { pane_id: "w9:p9" })).toEqual({ pane_id: "w1:p1" });
  });

  it("drops undefined values so they never reach the strict decoder", () => {
    expect(paneParams({ paneId: "w1:p1", generation: 1 }, { label: undefined })).toEqual({ pane_id: "w1:p1" });
  });
});
