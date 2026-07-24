import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CreateWorkspaceSheet } from "./create-sheets";
import { store } from "@/lib/store";
import * as api from "@/lib/api";
import { selectionStore } from "@/lib/selection";

beforeEach(() => {
  selectionStore.set(null);
  vi.spyOn(api, "listDirectories").mockResolvedValue({
    path: "/Users/dev/code",
    parent: "/Users/dev",
    entries: [{ name: "space-api", path: "/Users/dev/code/space-api" }],
  });
});
afterEach(() => vi.restoreAllMocks());

describe("CreateWorkspaceSheet", () => {
  it("submits workspace.create and navigates into result.root_pane.pane_id (M3)", async () => {
    // The backend returns root_pane as a Pane object, not a bare id.
    const runSpy = vi
      .spyOn(store, "runMutation")
      .mockResolvedValue({ request_id: "r", accepted: true, result: { workspace: { workspace_id: "w9" }, root_pane: { pane_id: "w9:p1" } } });

    render(<CreateWorkspaceSheet trigger={<button>open</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "open" }));

    await screen.findByRole("dialog");
    await userEvent.type(screen.getByLabelText("Label"), "new-space");
    await userEvent.click(screen.getByRole("button", { name: /create workspace/i }));

    await waitFor(() => expect(runSpy).toHaveBeenCalled());
    const [op, params] = runSpy.mock.calls[0];
    expect(op).toBe("workspace.create");
    expect(params).toMatchObject({ label: "new-space" });
    // Selection follows the newly created root pane.
    await waitFor(() => expect(selectionStore.get()).toBe("w9:p1"));
  });
});
