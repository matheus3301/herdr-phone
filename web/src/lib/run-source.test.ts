import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import * as api from "./api";
import { ApiError } from "./api";
import { runSource } from "./run-source";
import {
  makeCapabilities,
  makeRunContract,
  makeSnapshot,
  makeWireRun,
  makeWireRunsResponse,
  seedStore,
  updateStore,
} from "@/test/fixtures";

/** Subscribe the way a component does, so the source attaches to the app store. */
function watch(): () => void {
  return runSource.subscribe(() => {});
}

/** Wait until the source settles on a list of the expected size. */
async function settled(count: number) {
  await vi.waitFor(() => expect(runSource.getState().runs).toHaveLength(count));
  return runSource.getState();
}

beforeEach(() => {
  runSource.reset();
  seedStore({ ready: true, snapshot: null, capabilities: null, connection: "live" });
});
afterEach(() => {
  runSource.reset();
  vi.restoreAllMocks();
});

describe("old-relay fallback mode", () => {
  it("projects the snapshot and never calls the run routes", () => {
    const spy = vi.spyOn(api, "getRuns");
    seedStore({ snapshot: makeSnapshot(), capabilities: makeCapabilities() });
    const stop = watch();

    const state = runSource.getState();
    expect(state.fidelity).toBe("terminal-output");
    expect(spy).not.toHaveBeenCalled();
    // Internal, locally derived ids: never sent to the relay, never displayed
    // as a run id.
    expect(state.runs.map((r) => r.id)).toContain("w1:p1~g3");
    expect(state.runs.every((r) => r.origin === "snapshot")).toBe(true);
    expect(state.truncated).toBe(false);
    stop();
  });
});

describe("production run mode", () => {
  const capabilities = makeCapabilities({ runs: makeRunContract() });

  it("takes the run list, and its ids, from the relay", async () => {
    const spy = vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse());
    seedStore({ snapshot: makeSnapshot(), capabilities });
    const stop = watch();

    const state = await settled(1);
    expect(spy).toHaveBeenCalledTimes(1);
    expect(state.fidelity).toBe("observed");
    expect(state.runs[0].id).toBe("w1:p1@3");
    expect(state.runs[0].origin).toBe("relay");
    expect(state.runs[0].incarnation).toBe("0123456789abcdef");
    stop();
  });

  it("reports the relay's list truncation and the bound that applied", async () => {
    vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse({ truncated: true }));
    seedStore({ snapshot: makeSnapshot(), capabilities });
    const stop = watch();

    const state = await settled(1);
    expect(state.truncated).toBe(true);
    expect(state.maxRuns).toBe(200);
    stop();
  });

  it("refetches on a snapshot wakeup, and only on a change", async () => {
    const spy = vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse());
    const snapshot = makeSnapshot();
    seedStore({ snapshot, capabilities });
    const stop = watch();
    await settled(1);

    // An unrelated app-state change is not a wakeup.
    updateStore({ connection: "trouble" });
    await Promise.resolve();
    expect(spy).toHaveBeenCalledTimes(1);

    spy.mockResolvedValue(makeWireRunsResponse({ runs: [makeWireRun(), makeWireRun({ run_id: "w2:p1@2", pane_id: "w2:p1", status: "working" })] }));
    updateStore({ snapshot: { ...snapshot, hash: "h2" } });
    await settled(2);
    expect(spy).toHaveBeenCalledTimes(2);
    stop();
  });

  it("keeps the last good list and reports a failure statically", async () => {
    const spy = vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse());
    const snapshot = makeSnapshot();
    seedStore({ snapshot, capabilities });
    const stop = watch();
    await settled(1);

    spy.mockRejectedValue(new ApiError(503, "unavailable", "herdr unavailable"));
    updateStore({ snapshot: { ...snapshot, hash: "h3" } });
    await vi.waitFor(() => expect(runSource.getState().error).toBeTruthy());

    const state = runSource.getState();
    // A failed refresh must not empty the inbox, and must not silently swap in
    // snapshot-derived ids either.
    expect(state.runs).toHaveLength(1);
    expect(state.runs[0].origin).toBe("relay");
    expect(state.fidelity).toBe("observed");
    stop();
  });

  it("drops a projected list the moment the relay announces the contract", async () => {
    vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse());
    seedStore({ snapshot: makeSnapshot(), capabilities: makeCapabilities() });
    const stop = watch();
    expect(runSource.getState().runs[0].origin).toBe("snapshot");

    // Re-pairing against a newer relay: capabilities change under a live store.
    updateStore({ capabilities });
    const state = await settled(1);
    expect(state.runs.every((r) => r.origin === "relay")).toBe(true);
    stop();
  });

  it("ignores a contract version this build does not implement", () => {
    const spy = vi.spyOn(api, "getRuns");
    seedStore({
      snapshot: makeSnapshot(),
      capabilities: makeCapabilities({ runs: makeRunContract({ contractVersion: 2 }) }),
    });
    const stop = watch();
    // normalizeRunContract rejects the unknown version upstream of this store,
    // so the source must be in fallback mode with no run-route traffic.
    expect(runSource.getState().fidelity).toBe("terminal-output");
    expect(spy).not.toHaveBeenCalled();
    stop();
  });
});
