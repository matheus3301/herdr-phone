import { describe, it, expect, beforeEach } from "vitest";
import { selectionStore, resolveSelection } from "./selection";
import { makeSnapshot } from "@/test/fixtures";

describe("selection store", () => {
  beforeEach(() => selectionStore.set(null));

  it("stores and clears the selected pane id", () => {
    selectionStore.set("w1:p1");
    expect(selectionStore.get()).toBe("w1:p1");
    selectionStore.set(null);
    expect(selectionStore.get()).toBe(null);
  });

  it("notifies subscribers only on change", () => {
    let calls = 0;
    const off = selectionStore.subscribe(() => calls++);
    selectionStore.set("w1:p1");
    selectionStore.set("w1:p1");
    selectionStore.set("w2:p1");
    off();
    expect(calls).toBe(2);
  });
});

describe("resolveSelection", () => {
  const snap = makeSnapshot();

  it("resolves the selected pane with its tab and workspace", () => {
    const r = resolveSelection(snap, "w2:p1");
    expect(r.pane?.id).toBe("w2:p1");
    expect(r.tab?.id).toBe("w2:t1");
    expect(r.workspace?.id).toBe("w2");
  });

  it("falls back to the focused pane when selection is missing", () => {
    const r = resolveSelection(snap, "does-not-exist");
    expect(r.pane?.id).toBe("w1:p1");
  });

  it("falls back to the first pane when nothing is focused", () => {
    const r = resolveSelection(makeSnapshot({ focusedPaneId: null }), null);
    expect(r.pane?.id).toBe("w1:p1");
  });

  it("returns nulls with no snapshot", () => {
    expect(resolveSelection(null, "x")).toEqual({ workspace: null, tab: null, pane: null });
  });
});
