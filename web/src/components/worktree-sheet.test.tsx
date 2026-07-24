import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { WorktreeSheet } from "./worktree-sheet";
import { store } from "@/lib/store";
import * as api from "@/lib/api";
import { seedStore, makeSnapshot } from "@/test/fixtures";
import type { Worktree } from "@/lib/types";

const worktrees: Worktree[] = [
  { path: "/Users/dev/code/space-api", label: "auth", branch: "auth-refactor", isDetached: false, isPrunable: false, openWorkspaceId: "w1", removable: true },
  { path: "/Users/dev/code/experiment", label: "exp", branch: "experiment", isDetached: false, isPrunable: true, openWorkspaceId: null, removable: false },
];

beforeEach(() => {
  seedStore({ snapshot: makeSnapshot({ worktrees }), connection: "live" });
  vi.spyOn(api, "listDirectories").mockResolvedValue({ path: "/Users/dev/code", parent: "/Users/dev", entries: [] });
});
afterEach(() => vi.restoreAllMocks());

describe("WorktreeSheet", () => {
  it("lists worktrees and disables remove for a non-open one", async () => {
    render(<WorktreeSheet trigger={<button>wt</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "wt" }));
    expect(screen.getByText("auth-refactor")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /open the worktree to remove it/i })).toBeDisabled();
  });

  it("creates a worktree with a source repo cwd", async () => {
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    render(<WorktreeSheet trigger={<button>wt</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "wt" }));
    await userEvent.click(screen.getByRole("button", { name: /new worktree/i }));
    await userEvent.type(screen.getByLabelText("Branch"), "feature/x");
    await userEvent.click(screen.getByRole("button", { name: /create worktree/i }));
    await waitFor(() => expect(runSpy).toHaveBeenCalled());
    const [op, params] = runSpy.mock.calls[0];
    expect(op).toBe("worktree.create");
    expect(params).toMatchObject({ branch: "feature/x" });
    expect(params).toHaveProperty("cwd");
  });

  it("removes an open worktree with a confirmation nonce bound to its workspace", async () => {
    vi.spyOn(store, "prepareConfirmation").mockResolvedValue({ confirmation: "cnf", expiresUnixMs: Date.now() + 30_000 });
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
    render(<WorktreeSheet trigger={<button>wt</button>} />);
    await userEvent.click(screen.getByRole("button", { name: "wt" }));
    await userEvent.click(screen.getByRole("button", { name: /remove auth-refactor/i }));
    await screen.findByRole("alertdialog");
    await userEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    await waitFor(() =>
      expect(runSpy).toHaveBeenCalledWith("worktree.remove", { worktree_id: "w1" }, expect.objectContaining({ confirmation: "cnf" })),
    );
  });
});
