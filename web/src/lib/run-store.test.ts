import { describe, it, expect, vi } from "vitest";
import { observeRuns, RunStore } from "./run-store";
import { buildRuns } from "./run";
import { makeSnapshot } from "@/test/fixtures";

function runFor(name: string, overrides: Partial<{ status: "idle" | "working" | "blocked" | "done" | "unknown"; seq: number }> = {}) {
  const snapshot = makeSnapshot();
  snapshot.agents = snapshot.agents.map((a) =>
    a.name === name
      ? { ...a, status: overrides.status ?? a.status, stateChangeSeq: overrides.seq ?? a.stateChangeSeq }
      : a,
  );
  return buildRuns(snapshot).find((r) => r.agentName === name)!;
}

describe("RunStore — drafts", () => {
  it("keeps a draft per run and notifies only that partition", () => {
    const store = new RunStore();
    const a = vi.fn();
    const b = vi.fn();
    store.subscribe("run-a", a);
    store.subscribe("run-b", b);

    store.setDraft("run-a", "hello");
    expect(store.get("run-a").draft).toBe("hello");
    expect(store.get("run-b").draft).toBe("");
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).not.toHaveBeenCalled();
  });

  it("does not notify when the draft is unchanged", () => {
    const store = new RunStore();
    const cb = vi.fn();
    store.subscribe("r", cb);
    store.setDraft("r", "same");
    store.setDraft("r", "same");
    expect(cb).toHaveBeenCalledTimes(1);
  });
});

describe("RunStore — instruction delivery", () => {
  it("records a send as pending and settles it as accepted", () => {
    let t = 1000;
    const store = new RunStore(() => t);
    const id = store.beginSend("r", "continue");
    expect(store.get("r").instructions).toMatchObject([{ id, text: "continue", state: "pending", settledAt: null }]);

    t = 1500;
    store.settleSend("r", id, "accepted");
    expect(store.get("r").instructions[0]).toMatchObject({ state: "accepted", settledAt: 1500, error: null });
  });

  it("records an uncertain delivery with its message and never retries it", () => {
    const store = new RunStore();
    const id = store.beginSend("r", "deploy");
    store.settleSend("r", id, "delivery_unknown", "The relay did not answer.");
    const instruction = store.get("r").instructions[0];
    expect(instruction.state).toBe("delivery_unknown");
    expect(instruction.error).toBe("The relay did not answer.");
    // Exactly one instruction: nothing in the store resends on its own.
    expect(store.get("r").instructions).toHaveLength(1);
  });

  it("records a rejection", () => {
    const store = new RunStore();
    const id = store.beginSend("r", "x");
    store.settleSend("r", id, "rejected", "resource changed; refresh and retry");
    expect(store.get("r").instructions[0]).toMatchObject({ state: "rejected" });
  });

  it("adds a runline entry only for a delivered instruction", () => {
    const store = new RunStore();
    const accepted = store.beginSend("r", "one");
    store.settleSend("r", accepted, "accepted");
    const uncertain = store.beginSend("r", "two");
    store.settleSend("r", uncertain, "delivery_unknown", "unknown");
    const rejected = store.beginSend("r", "three");
    store.settleSend("r", rejected, "rejected", "no");

    const kinds = store.get("r").observed.map((e) => e.kind);
    expect(kinds).toEqual(["instruction"]);
  });

  it("lets the operator dismiss a settled instruction", () => {
    const store = new RunStore();
    const id = store.beginSend("r", "x");
    store.settleSend("r", id, "rejected", "no");
    store.dismiss("r", id);
    expect(store.get("r").instructions).toEqual([]);
  });

  it("bounds the instruction list", () => {
    const store = new RunStore();
    for (let i = 0; i < 80; i++) store.beginSend("r", `n${i}`);
    expect(store.get("r").instructions.length).toBeLessThanOrEqual(40);
    expect(store.get("r").instructions.at(-1)?.text).toBe("n79");
  });
});

describe("RunStore — observed transitions", () => {
  it("records nothing on first sight", () => {
    const store = new RunStore();
    store.observe(runFor("claude"));
    expect(store.get(runFor("claude").id).observed).toEqual([]);
  });

  it("records a transition once the status actually changes", () => {
    let t = 5000;
    const store = new RunStore(() => t);
    const before = runFor("claude", { status: "working", seq: 1 });
    store.observe(before);

    t = 6000;
    const after = { ...before, status: "blocked" as const, stateChangeSeq: 2 };
    store.observe(after);

    const observed = store.get(before.id).observed;
    expect(observed).toHaveLength(1);
    expect(observed[0]).toMatchObject({ kind: "status", status: "blocked", tone: "attention", at: 6000 });
    expect(observed[0].text).toMatch(/decision/i);
  });

  it("is inert for repeated identical snapshots", () => {
    const store = new RunStore();
    const run = runFor("claude", { status: "working", seq: 1 });
    store.observe(run);
    const cb = vi.fn();
    store.subscribe(run.id, cb);
    store.observe(run);
    store.observe(run);
    expect(cb).not.toHaveBeenCalled();
  });

  it("names an Updated transition without claiming success", () => {
    const store = new RunStore();
    const run = runFor("codex", { status: "working", seq: 1 });
    store.observe(run);
    store.observe({ ...run, status: "done", stateChangeSeq: 2 });
    const entry = store.get(run.id).observed[0];
    expect(entry.text).toBe("Background work settled");
    expect(entry.text).not.toMatch(/success|passed|complete/i);
  });

  it("exposes the last local observation time and bounds the feed", () => {
    let t = 0;
    const store = new RunStore(() => t);
    const run = runFor("claude", { status: "working", seq: 0 });
    store.observe(run);
    for (let i = 1; i <= 100; i++) {
      t = i * 1000;
      store.observe({ ...run, status: i % 2 ? "blocked" : "working", stateChangeSeq: i });
    }
    expect(store.get(run.id).observed.length).toBeLessThanOrEqual(60);
    expect(store.lastSeenAt(run.id)).toBe(100_000);
    expect(store.lastSeenAt("never-seen")).toBeNull();
  });
});

/**
 * Review MEDIUM 3.
 *
 * `observe` inserts a partition on first sight and a run id is per pane
 * *generation*, so a long supervising session accumulated one dead `RunState`
 * per recycled pane — each holding up to 40 raw instruction texts and 60
 * observed entries — and `forget()` had no production caller at all. Nothing
 * reached disk, but retaining instruction content for runs the contract has
 * already invalidated is exactly what freezing a run is supposed to end.
 */
describe("RunStore — partition lifetime", () => {
  it("releases a partition once its run leaves the live list", () => {
    const store = new RunStore();
    const claude = runFor("claude");
    const codex = runFor("codex");
    observeRuns([claude, codex], store);
    store.setDraft(claude.id, "an unsent instruction");
    expect(store.size()).toBe(2);

    // The pane behind `claude` is recycled: its run id is gone from the list.
    observeRuns([codex], store);
    expect(store.size()).toBe(1);
    expect(store.get(codex.id).lastSeq).not.toBeNull();
    // The dead partition's instruction content is not retained: reading the id
    // again yields a blank partition, not the old draft.
    expect(store.get(claude.id).draft).toBe("");
  });

  it("does not retain instruction text for an invalidated run", () => {
    const store = new RunStore();
    const claude = runFor("claude");
    observeRuns([claude], store);
    store.beginSend(claude.id, "deploy the thing");
    expect(store.get(claude.id).instructions).toHaveLength(1);

    observeRuns([], store);
    expect(store.size()).toBe(0);
  });

  it("keeps a watched partition, so a frozen run on screen is not blanked", () => {
    const store = new RunStore();
    const claude = runFor("claude");
    observeRuns([claude], store);
    store.beginSend(claude.id, "still being read");

    // The route is still mounted and subscribed: this is the frozen
    // invalidated-run view, and clearing it out from under the reader would
    // destroy the history they were sent there to look at.
    const unsubscribe = store.subscribe(claude.id, () => {});
    observeRuns([], store);
    expect(store.get(claude.id).instructions).toHaveLength(1);

    // Released as soon as they navigate away.
    unsubscribe();
    observeRuns([], store);
    expect(store.size()).toBe(0);
  });

  it("prune reports how many partitions it dropped", () => {
    const store = new RunStore();
    store.setDraft("a", "x");
    store.setDraft("b", "y");
    store.setDraft("c", "z");
    expect(store.prune(["b"])).toBe(2);
    expect(store.size()).toBe(1);
  });
});
