import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StartAgentSheet, AgentKeysSheet, AgentPromptSheet } from "./agent-actions";
import { store } from "@/lib/store";
import { seedStore, makeSnapshot, makeCapabilities } from "@/test/fixtures";
import type { Agent } from "@/lib/types";

const claude: Agent = makeSnapshot().agents.find((a) => a.name === "claude")!;

beforeEach(() => seedStore({ snapshot: makeSnapshot(), capabilities: makeCapabilities(), connection: "live" }));
afterEach(() => vi.restoreAllMocks());

describe("StartAgentSheet (C1)", () => {
  it("uses server kinds, suggests a unique valid name, and dispatches agent.start", async () => {
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    render(<StartAgentSheet paneId="w1:p2" trigger={<button>start</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "start" }));
    await screen.findByRole("dialog");

    // Kinds come from capabilities.
    await userEvent.click(screen.getByRole("button", { name: /opencode/i }));
    // "opencode" is taken by the fixture agent → suggestion increments.
    expect(screen.getByLabelText("Name")).toHaveValue("opencode-2");

    await userEvent.click(screen.getByRole("button", { name: /start agent/i }));
    await waitFor(() => expect(runSpy).toHaveBeenCalled());
    const [op, params, opts] = runSpy.mock.calls[0];
    expect(op).toBe("agent.start");
    expect(params).toMatchObject({ pane_id: "w1:p2", kind: "opencode", name: "opencode-2" });
    expect(opts).toMatchObject({ expectedGeneration: 1 });
  });

  it("blocks start on an invalid name", async () => {
    render(<StartAgentSheet paneId="w1:p2" trigger={<button>start</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "start" }));
    await userEvent.click(screen.getByRole("button", { name: /claude/i }));
    const name = screen.getByLabelText("Name");
    await userEvent.clear(name);
    await userEvent.type(name, "1bad");
    expect(screen.getByText(/lowercase letters/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /start agent/i })).toBeDisabled();
  });

  it("disables start when the backend reports no discoverable kinds", async () => {
    seedStore({ capabilities: makeCapabilities({ agentKinds: [], agentKindsAvailable: false }) });
    render(<StartAgentSheet paneId="w1:p2" trigger={<button>start</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "start" }));
    expect(screen.getByText(/no agent kinds were discovered/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /start agent/i })).toBeDisabled();
  });
});

describe("AgentKeysSheet (H3)", () => {
  it("dispatches validated logical keys via agent.send_keys", async () => {
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    render(<AgentKeysSheet agent={claude} trigger={<button>keys</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "keys" }));
    await screen.findByRole("dialog");
    await userEvent.click(screen.getByRole("button", { name: /send enter/i }));
    await waitFor(() => expect(runSpy).toHaveBeenCalledWith("agent.send_keys", { pane_id: "w1:p1", keys: ["enter"] }, expect.anything()));
  });
});

describe("AgentPromptSheet", () => {
  it("dispatches agent.prompt with the pane id", async () => {
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    render(<AgentPromptSheet agent={claude} trigger={<button>prompt</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "prompt" }));
    await userEvent.type(screen.getByLabelText("Message"), "continue");
    await userEvent.click(screen.getByRole("button", { name: /^send$/i }));
    await waitFor(() => expect(runSpy).toHaveBeenCalledWith("agent.prompt", { pane_id: "w1:p1", text: "continue" }, expect.anything()));
  });
});
