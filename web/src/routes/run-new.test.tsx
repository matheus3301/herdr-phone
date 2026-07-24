import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { StartRunRoute } from "./run-new";
import { store } from "@/lib/store";
import { launchStore } from "@/lib/launch";
import { makeCapabilities, makeSnapshot, seedStore } from "@/test/fixtures";
import type { MutationOperation, MutationResponse } from "@/lib/types";

/**
 * Start run — the launch receipt's delivery semantics.
 *
 * The point of these tests is review HIGH 1: a first instruction whose delivery
 * the relay could not confirm must be reported as uncertain, must be excluded
 * from the generic failed-step retry, and must expose only a separately
 * labelled, warned re-send. A duplicated instruction to a live shell is worse
 * than an unanswered one, and nothing undoes it.
 */

/**
 * A snapshot whose focused workspace has a free shell pane with a live
 * generation, so the orchestration reaches the prompt step without needing to
 * split anything. `agent.start` is generation-guarded, so the generation matters.
 */
function snapshotWithFreePane() {
  const snapshot = makeSnapshot();
  return {
    ...snapshot,
    panes: [
      ...snapshot.panes,
      {
        id: "w1:p9",
        workspaceId: "w1",
        tabId: "w1:t1",
        focused: false,
        zoomed: false,
        cwd: "/Users/dev/code/space-api",
        title: "zsh",
        agentKind: null,
        agentName: null,
        agentStatus: null,
        generation: 4,
        revision: 1,
        order: 2,
      },
    ],
  };
}

function mount() {
  seedStore({
    ready: true,
    snapshot: snapshotWithFreePane(),
    capabilities: makeCapabilities({ agentKinds: ["claude"], agentKindsAvailable: true }),
    connection: "live",
  });
  return render(
    <MemoryRouter initialEntries={["/runs/new"]}>
      <Routes>
        <Route path="/runs/new" element={<StartRunRoute />} />
        <Route path="/runs/:runId" element={<h1>run opened</h1>} />
      </Routes>
    </MemoryRouter>,
  );
}

/** Record every mutation and answer each operation from a scripted table. */
function scriptMutations(script: Partial<Record<MutationOperation, MutationResponse>>) {
  const calls: Array<{ op: MutationOperation; params: Record<string, unknown> }> = [];
  vi.spyOn(store, "runMutation").mockImplementation(async (op, params) => {
    calls.push({ op, params });
    return script[op] ?? { request_id: "r", accepted: true, result: {} };
  });
  return calls;
}

/** Fill the compose form for an existing workspace and launch. */
async function launchInExistingWorkspace(user: ReturnType<typeof userEvent.setup>) {
  // fireEvent.change commits the whole value in one React event, which is both
  // faster than per-keystroke typing and not what these tests are about.
  fireEvent.change(screen.getByLabelText("What should the agent do?"), { target: { value: "tidy reconnect" } });
  await user.click(screen.getByRole("radio", { name: /an existing workspace/i }));
  await user.click(screen.getByRole("radio", { name: "claude" }));
  fireEvent.change(screen.getByLabelText("Name it"), { target: { value: "claude-2" } });
  await user.click(screen.getByRole("button", { name: /^Start run$/ }));
}

const UNCERTAIN: MutationResponse = {
  request_id: "r",
  error: { code: "deadline_exceeded", message: "operation outcome uncertain", retryable: true },
};

const REFUSED: MutationResponse = {
  request_id: "r",
  error: { code: "bad_request", message: "prompt rejected", retryable: false },
};

describe("Start run — an uncertain first instruction", () => {
  beforeEach(() => {
    launchStore.reset();
    vi.spyOn(store, "canMutate").mockReturnValue(true);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    launchStore.reset();
  });

  it("reports delivery as unknown rather than as a plain failure", async () => {
    const user = userEvent.setup();
    scriptMutations({ "agent.prompt": UNCERTAIN });
    mount();
    await launchInExistingWorkspace(user);

    expect(
      await screen.findByRole("heading", { level: 1, name: /agent started, delivery unknown/i }),
    ).toBeInTheDocument();
    // The copy says what actually happened, and points at the console first.
    expect(screen.getByText(/may already have/i)).toBeInTheDocument();
    expect(screen.getByText(/check the console before sending it again/i)).toBeInTheDocument();
    // Announced to a screen reader as its own outcome, not as "failed".
    expect(screen.getByText("delivery unknown")).toBeInTheDocument();
  });

  it("does not offer the generic failed-step retry for it", async () => {
    const user = userEvent.setup();
    scriptMutations({ "agent.prompt": UNCERTAIN });
    mount();
    await launchInExistingWorkspace(user);
    await screen.findByRole("heading", { level: 1, name: /delivery unknown/i });

    expect(screen.queryByRole("button", { name: /retry the failed step/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /send the objective again/i })).toBeInTheDocument();
  });

  it("dispatches no further agent.prompt until the operator asks", async () => {
    const user = userEvent.setup();
    const calls = scriptMutations({ "agent.prompt": UNCERTAIN });
    mount();
    await launchInExistingWorkspace(user);
    await screen.findByRole("heading", { level: 1, name: /delivery unknown/i });

    const prompts = () => calls.filter((c) => c.op === "agent.prompt").length;
    expect(prompts()).toBe(1);
    // Settle any pending work; still exactly one send.
    await waitFor(() => expect(prompts()).toBe(1));

    await user.click(screen.getByRole("button", { name: /send the objective again/i }));
    await waitFor(() => expect(prompts()).toBe(2));
  });

  it("preserves the partial success and offers the run that is already live", async () => {
    const user = userEvent.setup();
    scriptMutations({ "agent.prompt": UNCERTAIN });
    mount();
    await launchInExistingWorkspace(user);
    await screen.findByRole("heading", { level: 1, name: /delivery unknown/i });

    // Every earlier step keeps its recorded outcome.
    expect(screen.getByText(/Using space-api/)).toBeInTheDocument();
    expect(screen.getByText(/Started claude as claude-2/)).toBeInTheDocument();
    // Review LOW 9: the agent is live and in the inbox, so the route to it exists.
    await user.click(screen.getByRole("button", { name: /open the run/i }));
    expect(await screen.findByRole("heading", { level: 1, name: "run opened" })).toBeInTheDocument();
  });

  it("clears to delivered when a deliberate re-send is accepted", async () => {
    const user = userEvent.setup();
    let first = true;
    vi.spyOn(store, "runMutation").mockImplementation(async (op) => {
      if (op === "agent.prompt" && first) {
        first = false;
        return UNCERTAIN;
      }
      return { request_id: "r", accepted: true, result: {} };
    });
    mount();
    await launchInExistingWorkspace(user);
    await screen.findByRole("heading", { level: 1, name: /delivery unknown/i });

    await user.click(screen.getByRole("button", { name: /send the objective again/i }));
    expect(await screen.findByRole("heading", { level: 1, name: /run started/i })).toBeInTheDocument();
    expect(screen.getByText("Objective delivered")).toBeInTheDocument();
  });
});

describe("Start run — a refused first instruction", () => {
  beforeEach(() => {
    launchStore.reset();
    vi.spyOn(store, "canMutate").mockReturnValue(true);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    launchStore.reset();
  });

  // A deterministic refusal is a different outcome: Herdr did not act, so the
  // generic retry is safe and is what the operator should be offered.
  it("is a plain failure the generic retry may repeat", async () => {
    const user = userEvent.setup();
    scriptMutations({ "agent.prompt": REFUSED });
    mount();
    await launchInExistingWorkspace(user);

    expect(await screen.findByRole("heading", { level: 1, name: /run partly started/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /retry the failed step/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /send the objective again/i })).not.toBeInTheDocument();
    // Still recoverable: the agent that did start is reachable.
    expect(screen.getByRole("button", { name: /open the run/i })).toBeInTheDocument();
  });
});
