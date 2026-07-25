import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { PaneActions } from "./pane-actions";
import { store } from "@/lib/store";
import { makeCapabilities, makeSnapshot, seedStore } from "@/test/fixtures";
import type { MutationOperation, Pane } from "@/lib/types";

/**
 * Pane actions — the parameter shape of every pane-scoped mutation.
 *
 * This sheet wires more of the mutation allowlist than anything else in the app,
 * and each operation has its own required argument names: `pane.swap` takes
 * `target_pane_id`, `pane.move` takes a typed `destination`, `pane.resize` takes
 * a direction and an amount. The relay decodes with `DisallowUnknownFields` and
 * rejects a pane-scoped call without a nonzero `expected_generation`, so getting
 * a name or a generation wrong here is a silent dead control in production. That
 * is what these tests pin — restoring the coverage the rewrite dropped
 * (review LOW 5).
 */

const snapshot = makeSnapshot();

function paneFrom(id: string): Pane {
  return snapshot.panes.find((p) => p.id === id)!;
}

interface Call {
  op: MutationOperation;
  params: Record<string, unknown>;
  expectedGeneration?: number;
}

function record(): Call[] {
  const calls: Call[] = [];
  vi.spyOn(store, "runMutation").mockImplementation(async (op, params, opts) => {
    calls.push({ op, params, expectedGeneration: opts?.expectedGeneration });
    return { request_id: "r", accepted: true, result: {} };
  });
  return calls;
}

async function openSheet(pane: Pane) {
  seedStore({ ready: true, snapshot, capabilities: makeCapabilities(), connection: "live" });
  render(
    <MemoryRouter>
      <PaneActions pane={pane} trigger={<button>Actions</button>} />
    </MemoryRouter>,
  );
  await userEvent.click(screen.getByRole("button", { name: "Actions" }));
  await screen.findByRole("dialog");
}

/** The one call for `op`, asserting it was dispatched exactly once. */
function only(calls: Call[], op: MutationOperation): Call {
  const matching = calls.filter((c) => c.op === op);
  expect(matching, `${op} dispatched once`).toHaveLength(1);
  return matching[0];
}

describe("PaneActions — pane-scoped parameter shapes", () => {
  beforeEach(() => {
    vi.spyOn(store, "canMutate").mockReturnValue(true);
  });
  afterEach(() => vi.restoreAllMocks());

  it("carries the canonical pane id and the live generation on every operation", async () => {
    const calls = record();
    const pane = paneFrom("w1:p1"); // generation 3 in the fixture
    await openSheet(pane);

    await userEvent.click(screen.getByRole("button", { name: /focus this pane on the mac/i }));
    const call = only(calls, "pane.focus");
    expect(call.params.pane_id).toBe("w1:p1");
    expect(call.expectedGeneration).toBe(3);
    // The dispatcher-preferred aliases must never be sent as dispatch identity.
    expect(call.params).not.toHaveProperty("target");
    expect(call.params).not.toHaveProperty("agent");
  });

  it("splits with an explicit direction", async () => {
    const calls = record();
    await openSheet(paneFrom("w1:p1"));

    await userEvent.click(screen.getByRole("button", { name: /split right/i }));
    expect(only(calls, "pane.split").params).toMatchObject({ pane_id: "w1:p1", direction: "right" });

    calls.length = 0;
    await userEvent.click(screen.getByRole("button", { name: /split down/i }));
    expect(only(calls, "pane.split").params).toMatchObject({ direction: "down" });
  });

  it("resizes with a direction and an amount", async () => {
    const calls = record();
    await openSheet(paneFrom("w1:p1"));

    await userEvent.click(screen.getByRole("button", { name: /widen/i }));
    expect(only(calls, "pane.resize").params).toMatchObject({ direction: "right", amount: 4 });

    calls.length = 0;
    await userEvent.click(screen.getByRole("button", { name: /taller/i }));
    expect(only(calls, "pane.resize").params).toMatchObject({ direction: "down", amount: 4 });
  });

  it("swaps by target_pane_id, naming a real sibling in the same tab", async () => {
    const calls = record();
    // w1:t1 holds only w1:p1 in the fixture, so add a sibling to swap with.
    const sibling: Pane = { ...paneFrom("w1:p2"), id: "w1:p8", tabId: "w1:t1", order: 1 };
    snapshot.panes.push(sibling);
    try {
      await openSheet(paneFrom("w1:p1"));
      await userEvent.click(screen.getByRole("button", { name: /^swap$/i }));
      expect(only(calls, "pane.swap").params).toMatchObject({
        pane_id: "w1:p1",
        target_pane_id: "w1:p8",
      });
    } finally {
      snapshot.panes = snapshot.panes.filter((p) => p.id !== "w1:p8");
    }
  });

  it("disables swap when the pane is alone in its tab", async () => {
    record();
    await openSheet(paneFrom("w1:p1"));
    expect(screen.getByRole("button", { name: /^swap$/i })).toBeDisabled();
  });

  it("moves with a typed destination for each variant", async () => {
    const calls = record();
    await openSheet(paneFrom("w1:p1"));

    await userEvent.click(screen.getByRole("button", { name: /move to a new tab/i }));
    expect(only(calls, "pane.move").params).toMatchObject({ destination: { type: "new_tab" } });

    calls.length = 0;
    await userEvent.click(screen.getByRole("button", { name: /move to a new workspace/i }));
    expect(only(calls, "pane.move").params).toMatchObject({ destination: { type: "new_workspace" } });
  });

  it("moves to a chosen existing tab by tab_id", async () => {
    const calls = record();
    await openSheet(paneFrom("w1:p1"));

    await userEvent.click(screen.getByRole("button", { name: /move to another tab…/i }));
    // w1:t2 is the other tab in this workspace.
    await userEvent.click(await screen.findByRole("button", { name: /tests/i }));
    expect(only(calls, "pane.move").params).toMatchObject({
      pane_id: "w1:p1",
      destination: { type: "tab", tab_id: "w1:t2" },
    });
  });

  it("zooms without extra parameters", async () => {
    const calls = record();
    await openSheet(paneFrom("w1:p1"));
    await userEvent.click(screen.getByRole("button", { name: /^zoom$/i }));
    expect(only(calls, "pane.zoom").params).toEqual({ pane_id: "w1:p1" });
  });
});

describe("PaneActions — a pane with no live generation", () => {
  beforeEach(() => {
    vi.spyOn(store, "canMutate").mockReturnValue(true);
  });
  afterEach(() => vi.restoreAllMocks());

  // Generation 0 can never match, so the relay would refuse every one of these.
  // Refusing locally, once, with a message the operator can act on beats
  // presenting a sheet full of controls that all fail.
  it("refuses to act at all rather than sending doomed requests", async () => {
    const calls = record();
    const stale: Pane = { ...paneFrom("w1:p1"), generation: 0 };
    await openSheet(stale);

    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /split right/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /close pane/i })).not.toBeInTheDocument();
    expect(calls).toEqual([]);
  });
});
