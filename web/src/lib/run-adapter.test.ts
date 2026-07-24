import { describe, it, expect, vi, afterEach } from "vitest";
import { classifySend, createRunAdapter, detectRunFidelity, RECENT_OUTPUT_LINES } from "./run-adapter";
import { ApiError } from "./api";
import * as api from "./api";
import { makeCapabilities } from "@/test/fixtures";

afterEach(() => vi.restoreAllMocks());

describe("capability gate", () => {
  it("fails closed to terminal output when no structured contract is advertised", () => {
    expect(detectRunFidelity(null)).toBe("terminal-output");
    expect(detectRunFidelity(makeCapabilities())).toBe("terminal-output");
  });

  it("requires both halves of the structured contract before upgrading", () => {
    expect(detectRunFidelity(makeCapabilities({ operations: ["run.send"] }))).toBe("terminal-output");
    expect(detectRunFidelity(makeCapabilities({ operations: ["run.send", "run.respond"] }))).toBe("structured");
  });

  it("never claims message support in fallback mode", () => {
    const adapter = createRunAdapter(makeCapabilities());
    expect(adapter.supportsMessages).toBe(false);
    expect(adapter.fidelity).toBe("terminal-output");
  });

  it("reports logical-key support from the allowlist", () => {
    expect(createRunAdapter(makeCapabilities({ operations: ["agent.send_keys"] })).supportsKeys).toBe(true);
    expect(createRunAdapter(makeCapabilities({ operations: [] })).supportsKeys).toBe(false);
  });
});

describe("recent output", () => {
  it("reads a bounded recent slice and normalises the pane id", async () => {
    const spy = vi
      .spyOn(api, "readPane")
      .mockResolvedValue({ pane_id: "w1:p1", source: "recent", lines: 40, content: "$ ok\n" });
    const tail = await createRunAdapter(makeCapabilities()).readRecentOutput("w1:p1", RECENT_OUTPUT_LINES);
    expect(spy).toHaveBeenCalledWith("w1:p1", "recent", RECENT_OUTPUT_LINES, undefined);
    expect(tail).toMatchObject({ paneId: "w1:p1", lines: 40, content: "$ ok\n" });
  });

  it("degrades to no output rather than throwing into the run view", async () => {
    vi.spyOn(api, "readPane").mockRejectedValue(new ApiError(404, "not_found", "pane not found"));
    expect(await createRunAdapter(makeCapabilities()).readRecentOutput("w1:p1", 40)).toBeNull();
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
