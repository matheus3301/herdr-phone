import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useMutations } from "./use-mutations";
import { store } from "@/lib/store";

afterEach(() => vi.restoreAllMocks());

function accepted() {
  return vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
}

describe("runPane — the only path to a pane-scoped mutation", () => {
  it("sends the canonical pane id and the current generation", async () => {
    const spy = accepted();
    const { result } = renderHook(() => useMutations());
    await act(async () => {
      await result.current.runPane("agent.prompt", { paneId: "w1:p1", generation: 3 }, { text: "continue" });
    });
    expect(spy).toHaveBeenCalledWith("agent.prompt", { pane_id: "w1:p1", text: "continue" }, {
      expectedGeneration: 3,
      confirmation: undefined,
    });
  });

  it("never sends the dispatcher-preferred `target` alias", async () => {
    const spy = accepted();
    const { result } = renderHook(() => useMutations());
    await act(async () => {
      await result.current.runPane("agent.rename", { paneId: "w1:p1", generation: 3 }, { target: "claude", name: "claude-2" });
    });
    expect(spy.mock.calls[0][1]).toEqual({ pane_id: "w1:p1", name: "claude-2" });
  });

  it("refuses locally when the generation is unknown, with an actionable message", async () => {
    const spy = accepted();
    const { result } = renderHook(() => useMutations());
    let response: unknown;
    await act(async () => {
      response = await result.current.runPane("agent.prompt", { paneId: "w1:p1", generation: 0 }, { text: "x" });
    });
    expect(spy).not.toHaveBeenCalled();
    expect(response).toMatchObject({ error: { code: "generation_missing", retryable: false } });
    expect(result.current.error).toMatch(/generation is unknown/i);
  });

  it("refuses when no pane is bound at all", async () => {
    const spy = accepted();
    const { result } = renderHook(() => useMutations());
    await act(async () => {
      await result.current.runPane("pane.focus", null);
    });
    expect(spy).not.toHaveBeenCalled();
  });
});

describe("run — defence in depth", () => {
  it("blocks a pane-scoped operation assembled without a generation", async () => {
    const spy = accepted();
    const { result } = renderHook(() => useMutations());
    let response: unknown;
    await act(async () => {
      response = await result.current.run("pane.close", { pane_id: "w1:p1" }, { confirmation: "cnf" });
    });
    expect(spy).not.toHaveBeenCalled();
    expect(response).toMatchObject({ error: { code: "generation_missing" } });
  });

  it("lets a workspace-scoped operation through without a generation", async () => {
    const spy = accepted();
    const { result } = renderHook(() => useMutations());
    await act(async () => {
      await result.current.run("workspace.rename", { workspace_id: "w1", label: "api" });
    });
    expect(spy).toHaveBeenCalled();
  });

  it("requires a confirmation for a destructive operation before any network call", async () => {
    const spy = accepted();
    const { result } = renderHook(() => useMutations());
    let response: unknown;
    await act(async () => {
      response = await result.current.run("workspace.close", { workspace_id: "w1" });
    });
    expect(spy).not.toHaveBeenCalled();
    expect(response).toMatchObject({ error: { code: "confirmation_required" } });
  });

  it("surfaces a relay error message and clears it on demand", async () => {
    vi.spyOn(store, "runMutation").mockResolvedValue({
      request_id: "r",
      error: { code: "generation_stale", message: "resource changed; refresh and retry", retryable: true },
    });
    const { result } = renderHook(() => useMutations());
    await act(async () => {
      await result.current.runPane("pane.zoom", { paneId: "w1:p1", generation: 3 });
    });
    expect(result.current.error).toBe("resource changed; refresh and retry");
    act(() => result.current.clearError());
    expect(result.current.error).toBeNull();
  });
});
