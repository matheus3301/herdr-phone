import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConfirmAction } from "./confirm-action";
import { store } from "@/lib/store";

afterEach(() => vi.restoreAllMocks());

describe("ConfirmAction — accessible destructive confirmation", () => {
  it("prepares a nonce and passes the `confirmation` to the mutation", async () => {
    vi.spyOn(store, "prepareConfirmation").mockResolvedValue({ confirmation: "cnf-1", expiresUnixMs: Date.now() + 30_000 });
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });

    render(
      <ConfirmAction
        operation="pane.close"
        resourceId="w1:p1"
        label="server"
        params={{ pane_id: "w1:p1" }}
        expectedGeneration={3}
        trigger={<button>close</button>}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "close" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/terminated/i);

    await userEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    await waitFor(() => expect(runSpy).toHaveBeenCalled());
    // The nonce and the mutation are bound to the same pane generation.
    expect(runSpy.mock.calls[0][2]).toMatchObject({ confirmation: "cnf-1", expectedGeneration: 3 });
  });

  it("refuses a pane-scoped destructive action with no generation, before any call", async () => {
    vi.spyOn(store, "prepareConfirmation").mockResolvedValue({ confirmation: "cnf-1", expiresUnixMs: Date.now() + 30_000 });
    const runSpy = vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });

    render(
      <ConfirmAction operation="pane.close" resourceId="w1:p1" label="server" params={{ pane_id: "w1:p1" }} trigger={<button>close</button>} />,
    );
    await userEvent.click(screen.getByRole("button", { name: "close" }));
    await screen.findByRole("alertdialog");
    await userEvent.click(screen.getByRole("button", { name: /^confirm$/i }));

    expect(runSpy).not.toHaveBeenCalled();
    expect(await screen.findByRole("alert")).toHaveTextContent(/generation is unknown/i);
  });

  it("escalates to the forced operation when the primary op is refused", async () => {
    vi.spyOn(store, "prepareConfirmation").mockResolvedValue({ confirmation: "cnf", expiresUnixMs: Date.now() + 30_000 });
    const runSpy = vi
      .spyOn(store, "runMutation")
      .mockResolvedValueOnce({ request_id: "r", error: { code: "internal", message: "worktree is dirty", retryable: false } })
      .mockResolvedValueOnce({ request_id: "r2", accepted: true, result: {} });

    render(
      <ConfirmAction
        operation="worktree.remove"
        resourceId="w1"
        label="auth"
        params={{ worktree_id: "w1" }}
        escalateOperation="worktree.remove_force"
        trigger={<button>rm</button>}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "rm" }));
    await screen.findByRole("alertdialog");

    // First confirm is refused → dialog advances to the force step.
    await userEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    await waitFor(() => expect(screen.getByRole("button", { name: /force remove/i })).toBeInTheDocument());
    expect(runSpy.mock.calls[0][0]).toBe("worktree.remove");

    await userEvent.click(screen.getByRole("button", { name: /force remove/i }));
    await waitFor(() => expect(runSpy).toHaveBeenCalledTimes(2));
    expect(runSpy.mock.calls[1][0]).toBe("worktree.remove_force");
  });
});
