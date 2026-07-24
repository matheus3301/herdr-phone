import { describe, it, expect } from "vitest";
import { requiresConfirmation, fallbackSummary } from "./confirm";

describe("confirmation policy", () => {
  it("requires confirmation for destructive structural ops", () => {
    expect(requiresConfirmation("workspace.close")).toBe(true);
    expect(requiresConfirmation("tab.close")).toBe(true);
    expect(requiresConfirmation("pane.close")).toBe(true);
    expect(requiresConfirmation("worktree.remove")).toBe(true);
    expect(requiresConfirmation("worktree.remove_force")).toBe(true);
  });

  it("does not require confirmation for non-destructive ops", () => {
    expect(requiresConfirmation("pane.split")).toBe(false);
    expect(requiresConfirmation("agent.prompt")).toBe(false);
    expect(requiresConfirmation("workspace.create")).toBe(false);
  });

  it("produces a human fallback summary (backend returns none)", () => {
    expect(fallbackSummary("pane.close", "server")).toMatch(/terminated/i);
    expect(fallbackSummary("worktree.remove_force", "auth")).toMatch(/discards uncommitted/i);
  });
});
