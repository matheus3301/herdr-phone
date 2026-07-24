import { describe, it, expect, beforeEach } from "vitest";
import {
  draftProblem,
  emptyDraft,
  initialSteps,
  LaunchStore,
  launchPartiallySucceeded,
  launchSucceeded,
  LAUNCH_STEPS,
  nextStep,
} from "./launch";

describe("draft validation", () => {
  const base = { ...emptyDraft(), objective: "fix reconnect", agentKind: "claude", agentName: "claude" };

  it("requires an objective first — intent before topology", () => {
    expect(draftProblem(emptyDraft())).toMatch(/describe what the agent should do/i);
  });

  it("requires a workspace choice appropriate to the target", () => {
    expect(draftProblem({ ...base, targetKind: "existing", workspaceId: null })).toMatch(/choose a workspace/i);
    expect(draftProblem({ ...base, targetKind: "new-workspace", workspaceLabel: "" })).toMatch(/name the new workspace/i);
    expect(draftProblem({ ...base, targetKind: "new-worktree", branch: "" })).toMatch(/name the branch/i);
  });

  it("requires an agent kind and a name", () => {
    expect(draftProblem({ ...base, workspaceId: "w1", agentKind: null })).toMatch(/choose an agent/i);
    expect(draftProblem({ ...base, workspaceId: "w1", agentName: "  " })).toMatch(/give the agent a name/i);
  });

  it("accepts a complete draft for each target kind", () => {
    expect(draftProblem({ ...base, targetKind: "existing", workspaceId: "w1" })).toBeNull();
    expect(draftProblem({ ...base, targetKind: "new-workspace", workspaceLabel: "api" })).toBeNull();
    expect(draftProblem({ ...base, targetKind: "new-worktree", branch: "feature/x" })).toBeNull();
  });
});

describe("step sequencing", () => {
  it("walks the four operations in order", () => {
    expect(LAUNCH_STEPS).toEqual(["workspace", "pane", "agent", "prompt"]);
    const steps = initialSteps();
    expect(nextStep(steps)?.id).toBe("workspace");
    steps[0].status = "done";
    expect(nextStep(steps)?.id).toBe("pane");
  });

  it("treats a skipped step as satisfied", () => {
    const steps = initialSteps();
    steps[0].status = "skipped";
    expect(nextStep(steps)?.id).toBe("pane");
  });

  it("recognises full and partial success", () => {
    const steps = initialSteps();
    steps.forEach((s) => (s.status = "done"));
    expect(launchSucceeded(steps)).toBe(true);
    expect(launchPartiallySucceeded(steps)).toBe(false);

    steps[3].status = "failed";
    expect(launchSucceeded(steps)).toBe(false);
    expect(launchPartiallySucceeded(steps)).toBe(true);
  });
});

describe("LaunchStore", () => {
  let store: LaunchStore;
  beforeEach(() => {
    store = new LaunchStore();
  });

  it("keeps the draft across a route dismissal, which is what makes it resumable", () => {
    store.patchDraft({ objective: "fix reconnect", agentKind: "claude" });
    // A different consumer reading the module-level store later sees the draft.
    expect(store.getState().draft.objective).toBe("fix reconnect");
    expect(store.getState().draft.agentKind).toBe("claude");
  });

  it("never discards a completed step when a later one fails", () => {
    store.setStep("workspace", { status: "done", detail: "Created workspace api" });
    store.recordCreated({ workspaceId: "w9", paneId: "w9:p1" });
    store.setStep("pane", { status: "done", detail: "Pane w9:p1" });
    store.setStep("agent", { status: "failed", error: "agent name in use" });
    store.setPhase("settled");

    const state = store.getState();
    expect(state.steps.map((s) => s.status)).toEqual(["done", "done", "failed", "pending"]);
    // Nothing created has been rolled back — the receipt still names it.
    expect(state.created).toMatchObject({ workspaceId: "w9", paneId: "w9:p1" });
  });

  it("retries only the failed step", () => {
    store.setStep("workspace", { status: "done", detail: "Using space-api" });
    store.setStep("pane", { status: "done", detail: "Pane w1:p2" });
    store.setStep("agent", { status: "failed", error: "boom" });
    store.prepareRetry();

    const state = store.getState();
    expect(state.phase).toBe("running");
    expect(state.steps.map((s) => s.status)).toEqual(["done", "done", "pending", "pending"]);
    expect(state.steps[2].error).toBeNull();
    // The completed work keeps its receipt line.
    expect(state.steps[0].detail).toBe("Using space-api");
    expect(nextStep(state.steps)?.id).toBe("agent");
  });

  it("resets to a clean compose state on demand", () => {
    store.patchDraft({ objective: "x" });
    store.setStep("workspace", { status: "done" });
    store.recordCreated({ workspaceId: "w9" });
    store.reset();
    expect(store.getState()).toMatchObject({ phase: "compose", created: {} });
    expect(store.getState().draft.objective).toBe("");
    expect(store.getState().steps.every((s) => s.status === "pending")).toBe(true);
  });

  it("notifies subscribers", () => {
    let calls = 0;
    const off = store.subscribe(() => calls++);
    store.patchDraft({ objective: "a" });
    store.setPhase("running");
    off();
    store.setPhase("settled");
    expect(calls).toBe(2);
  });
});
