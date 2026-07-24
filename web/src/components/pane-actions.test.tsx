import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PaneActions } from "./pane-actions";
import { store } from "@/lib/store";
import { seedStore, makeSnapshot, makeCapabilities } from "@/test/fixtures";
import type { Pane } from "@/lib/types";

const shellPane: Pane = {
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
  order: 1,
};
const agentPane: Pane = makeSnapshot().panes.find((p) => p.id === "w1:p1")!;

beforeEach(() => seedStore({ snapshot: makeSnapshot(), capabilities: makeCapabilities(), connection: "live" }));
afterEach(() => vi.restoreAllMocks());

describe("PaneActions", () => {
  it("offers Start agent on a shell pane (C1) but not on an agent pane", async () => {
    const { unmount } = render(<PaneActions pane={shellPane} trigger={<button>open</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByText("Start agent")).toBeInTheDocument();
    unmount();

    render(<PaneActions pane={agentPane} trigger={<button>open2</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "open2" }));
    expect(screen.queryByText("Start agent")).toBeNull();
  });

  it("splits a pane", async () => {
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    render(<PaneActions pane={shellPane} trigger={<button>open</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "open" }));
    await userEvent.click(screen.getByRole("button", { name: /split right/i }));
    expect(runSpy).toHaveBeenCalledWith("pane.split", { pane_id: "w1:p9", direction: "right" }, { expectedGeneration: 4 });
  });

  it("moves a pane to an existing tab (M4)", async () => {
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    render(<PaneActions pane={shellPane} trigger={<button>open</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "open" }));
    await userEvent.click(screen.getByRole("button", { name: /move → tab…/i }));
    // Target list shows the other tab in the workspace ("tests" = w1:t2).
    await userEvent.click(await screen.findByRole("button", { name: /tests/i }));
    await waitFor(() =>
      expect(runSpy).toHaveBeenCalledWith(
        "pane.move",
        { pane_id: "w1:p9", destination: { type: "tab", tab_id: "w1:t2" } },
        { expectedGeneration: 4 },
      ),
    );
  });

  it("exposes a Close pane action", async () => {
    render(<PaneActions pane={shellPane} trigger={<button>open</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByText("Close pane")).toBeInTheDocument();
  });
});
