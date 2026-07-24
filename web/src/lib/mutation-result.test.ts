import { describe, it, expect } from "vitest";
import { rootPaneId, splitPaneId } from "./mutation-result";
import type { MutationResponse } from "./types";

describe("mutation result readers", () => {
  it("reads root_pane.pane_id from a create result (M3)", () => {
    const res: MutationResponse = { request_id: "r", accepted: true, result: { workspace: { workspace_id: "w9" }, root_pane: { pane_id: "w9:p1" } } };
    expect(rootPaneId(res)).toBe("w9:p1");
  });

  it("reads pane.pane_id from a split result", () => {
    const res: MutationResponse = { request_id: "r", accepted: true, result: { pane: { pane_id: "w1:p7" } } };
    expect(splitPaneId(res)).toBe("w1:p7");
  });

  it("returns null for errors or missing fields", () => {
    expect(rootPaneId({ request_id: "r", error: { code: "x", message: "y", retryable: false } })).toBeNull();
    expect(rootPaneId({ request_id: "r", accepted: true, result: {} })).toBeNull();
    expect(rootPaneId(null)).toBeNull();
    expect(splitPaneId({ request_id: "r", accepted: true, result: { root_pane: { pane_id: "x" } } })).toBeNull();
  });
});
