import { describe, it, expect, vi, afterEach } from "vitest";
import {
  classifySend,
  createRunAdapter,
  detectRunFidelity,
  RECENT_OUTPUT_LINES,
  RUN_ERROR_MESSAGE,
} from "./run-adapter";
import { ApiError } from "./api";
import * as api from "./api";
import { makeCapabilities, makeRunContract, makeWireRun, makeWireRunCapabilities } from "@/test/fixtures";

afterEach(() => vi.restoreAllMocks());

const CONTRACT = makeCapabilities({ runs: makeRunContract() });
const LEGACY = makeCapabilities();

describe("capability gate", () => {
  it("fails closed to terminal output when no run contract is advertised", () => {
    expect(detectRunFidelity(null)).toBe("terminal-output");
    expect(detectRunFidelity(LEGACY)).toBe("terminal-output");
  });

  it("uses the run contract when the relay advertises it", () => {
    expect(detectRunFidelity(CONTRACT)).toBe("observed");
    const adapter = createRunAdapter(CONTRACT);
    expect(adapter.usesRunContract).toBe(true);
    expect(adapter.supportsObservedOutput).toBe(true);
  });

  it("only claims structured messages when the relay advertises them", () => {
    expect(createRunAdapter(CONTRACT).supportsMessages).toBe(false);
    expect(createRunAdapter(LEGACY).supportsMessages).toBe(false);
    expect(detectRunFidelity(makeCapabilities({ runs: makeRunContract({ structuredMessages: true }) }))).toBe(
      "structured",
    );
  });

  it("clamps the requested line count to the relay's bound", () => {
    expect(createRunAdapter(CONTRACT).outputLines).toBe(RECENT_OUTPUT_LINES);
    expect(createRunAdapter(makeCapabilities({ runs: makeRunContract({ maxOutputLines: 10 }) })).outputLines).toBe(10);
  });

  it("reports logical-key support from the allowlist", () => {
    expect(createRunAdapter(makeCapabilities({ operations: ["agent.send_keys"] })).supportsKeys).toBe(true);
    expect(createRunAdapter(makeCapabilities({ operations: [] })).supportsKeys).toBe(false);
  });
});

describe("production run mode — reading through the contract", () => {
  it("asserts the generation and reads the observed-output part", async () => {
    const spy = vi.spyOn(api, "getRun").mockResolvedValue({
      contract_version: 1,
      capabilities: makeWireRunCapabilities(),
      run: makeWireRun(),
      parts: [
        {
          type: "observed_terminal_output",
          source: "recent-unwrapped",
          format: "text",
          lines: 40,
          bytes: 6,
          truncated: false,
          text: "$ ok\n",
        },
      ],
    });

    const result = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(spy).toHaveBeenCalledWith("w1:p1", {
      expectedGeneration: 3,
      source: "recent-unwrapped",
      lines: RECENT_OUTPUT_LINES,
      signal: undefined,
    });
    expect(result).toMatchObject({
      kind: "ok",
      output: { origin: "run-contract", text: "$ ok\n", source: "recent-unwrapped", truncated: false },
    });
  });

  it("honours the relay's truncation flag rather than inventing one", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue({
      contract_version: 1,
      capabilities: makeWireRunCapabilities(),
      run: makeWireRun(),
      parts: [
        { type: "observed_terminal_output", source: "recent-unwrapped", format: "text", lines: 40, bytes: 3, truncated: true, text: "ail" },
      ],
    });
    const result = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(result.kind === "ok" && result.output.truncated).toBe(true);
  });

  it("ignores part types it does not understand instead of rendering them", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue({
      contract_version: 1,
      capabilities: makeWireRunCapabilities(),
      run: makeWireRun(),
      parts: [
        // A future relay may add types without bumping the contract version.
        { type: "assistant_message", source: "", format: "text", lines: 0, bytes: 4, truncated: false, text: "hello" },
        { type: "observed_terminal_output", source: "recent-unwrapped", format: "text", lines: 40, bytes: 2, truncated: false, text: "$ " },
      ],
    });
    const result = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(result.kind === "ok" && result.output.text).toBe("$ ");
    expect(result.kind === "ok" && result.output.ignoredPartTypes).toEqual(["assistant_message"]);
  });

  it("returns no text when the relay sends no observed part at all", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue({
      contract_version: 1,
      capabilities: makeWireRunCapabilities(),
      run: makeWireRun(),
      parts: [],
    });
    const result = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(result).toMatchObject({ kind: "ok", output: { text: "", lines: 0 } });
  });

  it("never reads through a pane with no live generation", async () => {
    const spy = vi.spyOn(api, "getRun");
    const result = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 0 });
    expect(spy).not.toHaveBeenCalled();
    expect(result).toMatchObject({ kind: "error", code: "generation_stale" });
  });

  it("says so, statically, when the contract advertises no observed output", async () => {
    const spy = vi.spyOn(api, "getRun");
    const caps = makeCapabilities({ runs: makeRunContract({ observedTerminalOutput: false }) });
    const result = await createRunAdapter(caps).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(spy).not.toHaveBeenCalled();
    expect(result).toMatchObject({ kind: "error", code: "unsupported", message: RUN_ERROR_MESSAGE.unsupported });
  });
});

describe("run read errors are static and distinct", () => {
  const cases: Array<[number, string, string]> = [
    [409, "generation_stale", RUN_ERROR_MESSAGE.generation_stale],
    [404, "run_unavailable", RUN_ERROR_MESSAGE.run_unavailable],
    [503, "unavailable", RUN_ERROR_MESSAGE.unavailable],
    [504, "deadline_exceeded", RUN_ERROR_MESSAGE.deadline_exceeded],
    [502, "run_read_failed", RUN_ERROR_MESSAGE.run_read_failed],
  ];

  for (const [status, code, message] of cases) {
    it(`maps ${code} to its own static message`, async () => {
      // The relay's own message can quote pane content, so it is never shown:
      // the UI answers from a static table keyed on the stable code.
      vi.spyOn(api, "getRun").mockRejectedValue(new ApiError(status, code, "pane said: rm -rf /secrets"));
      const result = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 3 });
      expect(result).toMatchObject({ kind: "error", code, message });
      expect(result.kind === "error" && result.message).not.toMatch(/secrets/);
    });
  }

  it("treats a stale generation, and only that, as invalidating the open run", async () => {
    vi.spyOn(api, "getRun").mockRejectedValue(new ApiError(409, "generation_stale", "pane changed"));
    const stale = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(stale.kind === "error" && stale.invalidates).toBe(true);

    vi.spyOn(api, "getRun").mockRejectedValue(new ApiError(502, "run_read_failed", "boom"));
    const failed = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(failed.kind === "error" && failed.invalidates).toBe(false);
  });

  it("maps an unrecognised relay code to the generic read failure", async () => {
    vi.spyOn(api, "getRun").mockRejectedValue(new ApiError(500, "kaboom", "kaboom"));
    const result = await createRunAdapter(CONTRACT).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(result).toMatchObject({ kind: "error", code: "run_read_failed" });
  });
});

describe("old-relay fallback mode", () => {
  it("reads the pane directly and labels the result as terminal output", async () => {
    const runSpy = vi.spyOn(api, "getRun");
    const paneSpy = vi
      .spyOn(api, "readPane")
      .mockResolvedValue({ pane_id: "w1:p1", source: "recent", lines: 40, content: "$ ok\n" });

    const result = await createRunAdapter(LEGACY).readRunOutput({ paneId: "w1:p1", generation: 3 });

    // The run routes are never probed on a relay that does not advertise them.
    expect(runSpy).not.toHaveBeenCalled();
    expect(paneSpy).toHaveBeenCalledWith("w1:p1", "recent", RECENT_OUTPUT_LINES, undefined);
    expect(result).toMatchObject({
      kind: "ok",
      output: { origin: "pane-read", source: "recent", lines: 1, text: "$ ok\n", truncated: false },
    });
  });

  it("degrades to a static message rather than throwing into the run view", async () => {
    vi.spyOn(api, "readPane").mockRejectedValue(new ApiError(404, "not_found", "pane not found"));
    const result = await createRunAdapter(LEGACY).readRunOutput({ paneId: "w1:p1", generation: 3 });
    expect(result).toMatchObject({ kind: "error", code: "run_read_failed" });
  });
});

describe("classifySend", () => {
  it("treats an accepted mutation as delivered", () => {
    expect(classifySend({ request_id: "r", accepted: true, result: {} })).toEqual({ kind: "accepted" });
  });

  it("treats a deterministic refusal as rejected, so the draft is kept", () => {
    const outcome = classifySend({
      request_id: "r",
      error: { code: "generation_stale", message: "resource changed; refresh and retry", retryable: false },
    });
    expect(outcome).toEqual({ kind: "rejected", code: "generation_stale", message: "resource changed; refresh and retry" });
  });

  it("treats a retryable relay failure as delivery-unknown, never as a rejection", () => {
    // Herdr may already have the prompt; claiming it failed would invite a
    // duplicate instruction into a live shell.
    const outcome = classifySend({
      request_id: "r",
      error: { code: "deadline_exceeded", message: "operation outcome uncertain", retryable: true },
    });
    expect(outcome.kind).toBe("delivery_unknown");
  });

  it("treats a network timeout as delivery-unknown", () => {
    const outcome = classifySend(null, new ApiError(0, "timeout", "The relay did not respond in time.", true));
    expect(outcome.kind).toBe("delivery_unknown");
  });

  it("treats a non-retryable transport error as rejected", () => {
    const outcome = classifySend(null, new ApiError(403, "forbidden", "nope", false));
    expect(outcome).toMatchObject({ kind: "rejected", code: "forbidden" });
  });

  it("treats an absent response as delivery-unknown", () => {
    expect(classifySend(null).kind).toBe("delivery_unknown");
    expect(classifySend(null, new Error("boom")).kind).toBe("delivery_unknown");
  });
});
