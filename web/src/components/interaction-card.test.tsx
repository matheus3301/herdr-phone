import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { InteractionCard } from "./interaction-card";
import { store } from "@/lib/store";
import { buildRuns } from "@/lib/run";
import { makeCapabilities, makeInterpretingRunContract, makeSnapshot, seedStore } from "@/test/fixtures";
import type { InterpretedInteraction } from "@/lib/interpreted";

/**
 * The interaction card owns the one place this feature can do damage: turning a
 * heuristic reading of a screen into a keystroke delivered to a live agent.
 *
 * The rules under test (SPEC §12.2, §21):
 *  - nothing is sent on the first tap,
 *  - the exact key is shown before it is sent,
 *  - a prompt the relay marked unanswerable offers no send affordance at all,
 *  - the delivered payload is the literal key and nothing else.
 */

const run = buildRuns(makeSnapshot()).find((r) => r.agentName === "claude")!;

function claudeApproval(overrides: Partial<InterpretedInteraction> = {}): InterpretedInteraction {
  return {
    parser: "claude",
    kind: "approval",
    title: "Bash command",
    detail: ['echo "hello fixture" >> notes.txt', "Append line to notes.txt"],
    question: "Do you want to proceed?",
    answerable: true,
    options: [
      { label: "Yes", sendKey: "1" },
      { label: "Yes, and always allow access to sandbox/ from this project", sendKey: "2" },
      { label: "No", sendKey: "3" },
    ],
    diff: [],
    ...overrides,
  };
}

function openCodeApproval(): InterpretedInteraction {
  return {
    parser: "opencode",
    kind: "approval",
    title: "Edit /tmp/sandbox/notes.txt",
    detail: [],
    question: "Permission required",
    answerable: false,
    options: [{ label: "Allow once", sendKey: null }, { label: "Allow always", sendKey: null }, { label: "Reject", sendKey: null }],
    diff: [
      { line: 1, op: "context", text: "sample file for the fixture capture" },
      { line: 2, op: "add", text: "hello fixture" },
    ],
  };
}

function mount(interaction: InterpretedInteraction) {
  seedStore({
    ready: true,
    snapshot: makeSnapshot(),
    capabilities: makeCapabilities({ runs: makeInterpretingRunContract() }),
    connection: "live",
  });
  return render(
    <MemoryRouter>
      <InteractionCard run={run} interaction={interaction} />
    </MemoryRouter>,
  );
}

/**
 * store.runMutation is the single seam every pane mutation goes through, so
 * asserting on it proves what would actually reach the relay. Mirrors the helper
 * in use-mutations.test.tsx.
 */
function mockMutation() {
  return vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
}

describe("InteractionCard", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("labels the reading as experimental and names the parser", () => {
    mount(claudeApproval());
    expect(screen.getByText(/experimental reading/i)).toBeInTheDocument();
    expect(screen.getByText(/not the agent's own messages/i)).toBeInTheDocument();
  });

  it("shows the question and the detail the prompt carried", () => {
    mount(claudeApproval());
    expect(screen.getByText("Do you want to proceed?")).toBeInTheDocument();
    expect(screen.getByText(/echo "hello fixture" >> notes.txt/)).toBeInTheDocument();
  });

  it("sends nothing on the first tap", async () => {
    const mutate = mockMutation();
    const user = userEvent.setup();
    mount(claudeApproval());

    await user.click(screen.getByRole("button", { name: /Yes$/ }));

    // A confirmation step appeared, and no mutation has been attempted.
    expect(await screen.findByRole("heading", { name: /Send this answer/i })).toBeInTheDocument();
    expect(mutate).not.toHaveBeenCalled();
  });

  it("shows the exact key before it is sent", async () => {
    const user = userEvent.setup();
    mount(claudeApproval());

    await user.click(screen.getByRole("button", { name: /Yes, and always allow/ }));

    // Scope to the confirmation sheet: the option label also appears on the card
    // behind it, and the assertion is about what the confirm step shows.
    const sheet = await screen.findByRole("dialog");
    expect(within(sheet).getByText(/Key delivered/i)).toBeInTheDocument();
    expect(within(sheet).getByText('"2"')).toBeInTheDocument();
    expect(within(sheet).getByText(/Yes, and always allow access to sandbox\//)).toBeInTheDocument();
  });

  it("delivers the literal key through agent.send_keys once confirmed", async () => {
    const mutate = mockMutation();
    const user = userEvent.setup();
    mount(claudeApproval());

    await user.click(screen.getByRole("button", { name: /3\s*No/ }));
    await user.click(await screen.findByRole("button", { name: "Send" }));

    await waitFor(() => expect(mutate).toHaveBeenCalledTimes(1));
    const [operation, params, opts] = mutate.mock.calls[0];
    expect(operation).toBe("agent.send_keys");
    // Exactly the one literal key, and nothing else.
    expect(params).toEqual({ pane_id: run.paneId, keys: ["3"] });
    // The generation guard travels with every mutation.
    expect(opts?.expectedGeneration).toBe(run.generation);
  });

  it("can be cancelled without sending", async () => {
    const mutate = mockMutation();
    const user = userEvent.setup();
    mount(claudeApproval());

    await user.click(screen.getByRole("button", { name: /Yes$/ }));
    await user.click(await screen.findByRole("button", { name: "Cancel" }));

    expect(mutate).not.toHaveBeenCalled();
  });

  it("offers no send affordance for an unanswerable prompt", async () => {
    mount(openCodeApproval());

    // The labels are shown as text, not as buttons.
    expect(screen.getByText(/Allow once/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Allow once/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Reject/ })).not.toBeInTheDocument();

    // And the reason is explained, with a route to the surface that can answer.
    expect(screen.getByText(/can't be answered from your phone/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open console/i })).toBeInTheDocument();
  });

  it("renders the diff an approval is asking about", () => {
    mount(openCodeApproval());
    expect(screen.getByText("hello fixture")).toBeInTheDocument();
    expect(screen.getByText("sample file for the fixture capture")).toBeInTheDocument();
  });

  it("distinguishes a question from an approval", () => {
    mount(claudeApproval({ kind: "question", options: [{ label: "Alpha", sendKey: "1" }, { label: "Beta", sendKey: "2" }] }));
    expect(screen.getByText(/asking you something/i)).toBeInTheDocument();
    expect(screen.queryByText(/needs permission/i)).not.toBeInTheDocument();
  });
});
