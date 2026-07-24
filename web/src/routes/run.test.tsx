import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { RunRoute } from "./run";
import { store } from "@/lib/store";
import * as api from "@/lib/api";
import { runStore } from "@/lib/run-store";
import { makeCapabilities, makeSnapshot, seedStore } from "@/test/fixtures";
import type { Snapshot } from "@/lib/types";

const RUN_ID = "w1:p1~g3";

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

beforeEach(() => {
  runStore.forget(RUN_ID);
  vi.spyOn(api, "readPane").mockResolvedValue({ pane_id: "w1:p1", source: "recent", lines: 40, content: "$ ready\n" });
});
afterEach(() => vi.restoreAllMocks());

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
    // The worktree branch and the tab happen to share a name here.
    expect(screen.getAllByText("auth-refactor").length).toBeGreaterThan(0);
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
    await userEvent.type(field, "continue");
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));

    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(spy.mock.calls[0][0]).toBe("agent.prompt");
    expect(spy.mock.calls[0][1]).toEqual({ pane_id: "w1:p1", text: "continue" });
    expect(spy.mock.calls[0][2]).toMatchObject({ expectedGeneration: 3 });
    expect(await screen.findByText("Delivered")).toBeInTheDocument();
  });

  it("keeps the draft and explains a stale-generation rejection", async () => {
    vi.spyOn(store, "runMutation").mockResolvedValue({
      request_id: "r",
      error: { code: "generation_stale", message: "resource changed; refresh and retry", retryable: false },
    });
    mount();
    const field = await screen.findByLabelText(/instruction for claude/i);
    await userEvent.type(field, "continue");
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));

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
    await userEvent.type(await screen.findByLabelText(/instruction for claude/i), "deploy");
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));

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
