import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { RunRoute } from "./run";
import { store } from "@/lib/store";
import * as api from "@/lib/api";
import { ApiError } from "@/lib/api";
import { runStore } from "@/lib/run-store";
import { runSource } from "@/lib/run-source";
import {
  makeCapabilities,
  makeRunContract,
  makeSnapshot,
  makeWireRun,
  makeWireRunCapabilities,
  makeWireRunsResponse,
  seedStore,
  updateStore,
} from "@/test/fixtures";
import type { Snapshot, WireObservedOutputPart } from "@/lib/types";

/** The fallback's internal run id: pane plus generation. */
const RUN_ID = "w1:p1~g3";
/** The relay's authoritative run id for the same pane incarnation. */
const RELAY_RUN_ID = "w1:p1@3#0123456789abcdef";

function mount(runId = RUN_ID, snapshot: Snapshot | null = makeSnapshot()) {
  seedStore({ ready: true, snapshot, capabilities: makeCapabilities(), connection: "live" });
  return render(
    <MemoryRouter initialEntries={[`/runs/${encodeURIComponent(runId)}`]}>
      <Routes>
        <Route path="/runs/:runId" element={<RunRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

/** Mount against a relay that advertises the structured run contract. */
function mountWithContract(runId = RELAY_RUN_ID, snapshot: Snapshot | null = makeSnapshot()) {
  seedStore({
    ready: true,
    snapshot,
    capabilities: makeCapabilities({ runs: makeRunContract() }),
    connection: "live",
  });
  return render(
    <MemoryRouter initialEntries={[`/runs/${encodeURIComponent(runId)}`]}>
      <Routes>
        <Route path="/runs/:runId" element={<RunRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

function observedPart(overrides: Partial<WireObservedOutputPart> = {}): WireObservedOutputPart {
  return {
    type: "observed_terminal_output",
    source: "recent-unwrapped",
    format: "text",
    lines: 40,
    bytes: 9,
    truncated: false,
    text: "$ ready\n",
    ...overrides,
  };
}

function runDetail(parts: WireObservedOutputPart[] = [observedPart()]) {
  return {
    contract_version: 1,
    capabilities: makeWireRunCapabilities(),
    run: makeWireRun(),
    parts,
  };
}

beforeEach(() => {
  runStore.forget(RUN_ID);
  runStore.forget(RELAY_RUN_ID);
  runSource.reset();
  vi.spyOn(api, "readPane").mockResolvedValue({ pane_id: "w1:p1", source: "recent", lines: 40, content: "$ ready\n" });
});
afterEach(() => {
  runSource.reset();
  vi.restoreAllMocks();
});

describe("Run detail — opening is read-only", () => {
  it("does not move Herdr focus when the run is opened", async () => {
    const spy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    mount();
    await screen.findByRole("heading", { level: 1, name: "claude" });
    await waitFor(() => expect(api.readPane).toHaveBeenCalled());
    // No agent.focus, no pane.focus, no workspace.focus — reading remote state
    // must never change what the operator's Mac is looking at.
    expect(spy).not.toHaveBeenCalled();
  });

  it("offers focusing the agent as a separate, explicit action", async () => {
    const spy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    mount();
    await userEvent.click(await screen.findByRole("button", { name: /actions for claude/i }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /focus this agent on the mac/i }));
    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(spy.mock.calls[0][0]).toBe("agent.focus");
    expect(spy.mock.calls[0][1]).toEqual({ pane_id: "w1:p1" });
    expect(spy.mock.calls[0][2]).toMatchObject({ expectedGeneration: 3 });
  });
});

describe("Run detail — execution context", () => {
  it("shows the exact pane, generation, and agent", async () => {
    mount();
    await userEvent.click(await screen.findByRole("button", { name: /show full execution context/i }));
    expect(screen.getByText("w1:p1")).toBeInTheDocument();
    expect(screen.getByText("claude (claude)")).toBeInTheDocument();
    expect(screen.getAllByText("auth-refactor").length).toBeGreaterThan(0);
    // Checkout provenance is named for what it is, and a linked worktree says so.
    expect(screen.getByText("Linked worktree")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("labels the pane read honestly and never as an agent message", async () => {
    mount();
    expect(await screen.findByText(/recent terminal output/i)).toBeInTheDocument();
    expect(screen.getByText(/not a transcript/i)).toBeInTheDocument();
    expect(await screen.findByText(/\$ ready/)).toBeInTheDocument();
  });

  it("says plainly that observed activity is what this device saw", async () => {
    mount();
    expect(await screen.findByRole("heading", { name: /observed activity/i })).toBeInTheDocument();
    expect(screen.getByText(/herdr publishes no agent messages/i)).toBeInTheDocument();
  });
});

describe("Run detail — instruction delivery", () => {
  it("sends the canonical pane id and generation, and records the instruction", async () => {
    const spy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    mount();
    const field = await screen.findByLabelText(/instruction for claude/i);
    // One change event rather than eight keystrokes: per-keystroke typing through
    // jsdom is the slowest path in the suite and scales with parallel worker
    // load, which is what made these three cases nondeterministic. The delivery
    // states under test do not depend on how the text arrived.
    fireEvent.change(field, { target: { value: "continue" } });
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));

    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(spy.mock.calls[0][0]).toBe("agent.prompt");
    expect(spy.mock.calls[0][1]).toEqual({ pane_id: "w1:p1", text: "continue" });
    expect(spy.mock.calls[0][2]).toMatchObject({ expectedGeneration: 3 });
    expect(await screen.findByText("Delivered")).toBeInTheDocument();
  });

  it("keeps the draft and explains a stale-generation rejection", async () => {
    const spy = vi.spyOn(store, "runMutation").mockResolvedValue({
      request_id: "r",
      error: { code: "generation_stale", message: "resource changed; refresh and retry", retryable: false },
    });
    mount();
    const field = await screen.findByLabelText(/instruction for claude/i);
    fireEvent.change(field, { target: { value: "continue" } });
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));

    // Await the dispatch before asserting on what it rendered, so the assertion
    // is not racing the mutation it depends on.
    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(await screen.findByText("Not sent")).toBeInTheDocument();
    expect(screen.getByText(/resource changed/i)).toBeInTheDocument();
    expect(field).toHaveValue("continue");
  });

  it("surfaces delivery-unknown with an explicit choice and no automatic retry", async () => {
    const spy = vi.spyOn(store, "runMutation").mockResolvedValue({
      request_id: "r",
      error: { code: "deadline_exceeded", message: "operation outcome uncertain", retryable: true },
    });
    mount();
    fireEvent.change(await screen.findByLabelText(/instruction for claude/i), { target: { value: "deploy" } });
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));

    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(await screen.findByText("Delivery unknown")).toBeInTheDocument();
    expect(screen.getByText(/may already have received this/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /send again/i })).toBeInTheDocument();
    // One attempt. Nothing retried it for the operator.
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("blocks the composer, in words, when the pane has no lifecycle generation", async () => {
    const snapshot = makeSnapshot();
    snapshot.panes = snapshot.panes.map((p) => (p.id === "w1:p1" ? { ...p, generation: 0 } : p));
    const spy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    mount("w1:p1~g0", snapshot);

    // The agent is still shown — hiding it would be worse — but every mutation
    // path is closed and says why, instead of sending requests the relay refuses.
    await screen.findByRole("heading", { level: 1, name: "claude" });
    expect(screen.getByText(/generation is unknown/i)).toBeInTheDocument();
    const field = screen.getByLabelText(/instruction for claude/i);
    expect(field).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));
    expect(spy).not.toHaveBeenCalled();
  });
});

describe("Run detail — production run mode", () => {
  beforeEach(() => {
    vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse());
  });

  it("opens the relay's run by its authoritative id and reads it generation-guarded", async () => {
    const detail = vi.spyOn(api, "getRun").mockResolvedValue(runDetail());
    mountWithContract();

    await screen.findByRole("heading", { level: 1, name: "claude" });
    await waitFor(() => expect(detail).toHaveBeenCalled());
    expect(detail.mock.calls[0][0]).toBe("w1:p1");
    expect(detail.mock.calls[0][1]).toMatchObject({ expectedGeneration: 3, source: "recent-unwrapped" });
    // The legacy pane read is not used when the contract is advertised.
    expect(api.readPane).not.toHaveBeenCalled();
  });

  it("renders the observed part as terminal output and never as an agent message", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue(runDetail());
    mountWithContract();

    expect(await screen.findByText(/recent terminal output/i)).toBeInTheDocument();
    expect(await screen.findByText(/\$ ready/)).toBeInTheDocument();
    expect(screen.getByText(/not the agent's own messages/i)).toBeInTheDocument();
    // Nothing on the page claims a semantic structure the relay says it lacks.
    expect(screen.queryByText(/assistant/i)).toBeNull();
    expect(screen.queryByRole("heading", { name: /messages|conversation|tool calls|diff|test results/i })).toBeNull();
  });

  it("ignores a part type it does not understand instead of rendering it", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue(
      runDetail([
        { type: "assistant_message", source: "", format: "text", lines: 0, bytes: 5, truncated: false, text: "hello" },
        observedPart(),
      ]),
    );
    mountWithContract();

    expect(await screen.findByText(/\$ ready/)).toBeInTheDocument();
    expect(screen.queryByText("hello")).toBeNull();
    expect(screen.getByText(/1 part this app does not understand/i)).toBeInTheDocument();
  });

  it("says when the relay truncated the output", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue(runDetail([observedPart({ truncated: true })]));
    mountWithContract();
    expect(await screen.findByText(/older output was dropped/i)).toBeInTheDocument();
  });

  it("shows a static message for a read failure, never the relay's own text", async () => {
    vi.spyOn(api, "getRun").mockRejectedValue(new ApiError(502, "run_read_failed", "herdr said: cat /etc/passwd"));
    mountWithContract();
    expect(await screen.findByText(/herdr could not read this pane/i)).toBeInTheDocument();
    expect(screen.queryByText(/etc\/passwd/)).toBeNull();
  });

  it("still sends the canonical pane id and generation on an instruction", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue(runDetail());
    const spy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    mountWithContract();

    const field = await screen.findByLabelText(/instruction for claude/i);
    // One change event rather than eight keystrokes: per-keystroke typing through
    // jsdom is the slowest path in the suite and scales with parallel worker
    // load, which is what made these three cases nondeterministic. The delivery
    // states under test do not depend on how the text arrived.
    fireEvent.change(field, { target: { value: "continue" } });
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));

    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(spy.mock.calls[0][0]).toBe("agent.prompt");
    expect(spy.mock.calls[0][1]).toEqual({ pane_id: "w1:p1", text: "continue" });
    expect(spy.mock.calls[0][2]).toMatchObject({ expectedGeneration: 3 });
  });

  it("freezes the run when the relay reports the generation has moved on", async () => {
    vi.spyOn(api, "getRun").mockRejectedValue(new ApiError(409, "generation_stale", "pane changed; refresh and retry"));
    mountWithContract();

    // The run is not rebound to whoever occupies the pane now.
    expect(
      await screen.findByRole("heading", { level: 1, name: /pane was replaced|agent has ended/i }),
    ).toBeInTheDocument();
    expect(screen.getByText("w1:p1")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /open console/i })).toBeInTheDocument();
  });

  it("freezes an open run when the relay replaces its incarnation, and offers the successor", async () => {
    const list = vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse());
    vi.spyOn(api, "getRun").mockResolvedValue(runDetail());
    const snapshot = makeSnapshot();
    mountWithContract(RELAY_RUN_ID, snapshot);
    await screen.findByRole("heading", { level: 1, name: "claude" });

    // Herdr recycles the pane: same id, new generation, new incarnation, and
    // therefore a new authoritative run id.
    list.mockResolvedValue(
      makeWireRunsResponse({
        runs: [makeWireRun({ run_id: "w1:p1@4", pane_generation: 4, agent_incarnation: "ffff0000ffff0000" })],
      }),
    );
    updateStore({ snapshot: { ...snapshot, hash: "h2" } });

    expect(
      await screen.findByRole("heading", { level: 1, name: /pane was replaced|agent has ended/i }),
    ).toBeInTheDocument();
    // The successor is offered by its authoritative id, not a derived one, and
    // only as a deliberate choice.
    await waitFor(() =>
      expect(screen.getByRole("link", { name: /open the new occupant/i })).toHaveAttribute("href", "/runs/w1%3Ap1%404"),
    );
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("does not claim a run is gone while the list is still loading", async () => {
    let release: (v: ReturnType<typeof makeWireRunsResponse>) => void = () => {};
    vi.spyOn(api, "getRuns").mockReturnValue(
      new Promise((resolve) => {
        release = resolve;
      }),
    );
    vi.spyOn(api, "getRun").mockResolvedValue(runDetail());
    mountWithContract();

    expect(await screen.findByRole("heading", { level: 1, name: /loading this run/i })).toBeInTheDocument();
    release(makeWireRunsResponse());
    expect(await screen.findByRole("heading", { level: 1, name: "claude" })).toBeInTheDocument();
  });
});

describe("Run detail — invalidation", () => {
  it("freezes a run whose pane was recycled and offers the new occupant", async () => {
    mount("w1:p1~g2");
    expect(await screen.findByRole("heading", { level: 1, name: /pane was replaced/i })).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    const link = screen.getByRole("link", { name: /open the new occupant/i });
    expect(link).toHaveAttribute("href", "/runs/w1%3Ap1~g3");
  });

  it("reports an ended agent and still offers the console", async () => {
    const snapshot = makeSnapshot();
    snapshot.agents = snapshot.agents.filter((a) => a.paneId !== "w1:p1");
    snapshot.panes = snapshot.panes.filter((p) => p.id !== "w1:p1");
    mount(RUN_ID, snapshot);
    expect(await screen.findByRole("heading", { level: 1, name: /agent has ended/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /open console/i })).toHaveAttribute("href", "/console/w1%3Ap1");
  });
});
