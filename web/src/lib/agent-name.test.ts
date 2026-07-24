import { describe, it, expect } from "vitest";
import { isValidAgentName, sanitizeAgentName, suggestAgentName } from "./agent-name";

describe("agent name rules (mirror of backend ValidAgentName)", () => {
  it("accepts valid names", () => {
    expect(isValidAgentName("claude")).toBe(true);
    expect(isValidAgentName("codex-2")).toBe(true);
    expect(isValidAgentName("a_b-9")).toBe(true);
  });

  it("rejects invalid names", () => {
    expect(isValidAgentName("")).toBe(false);
    expect(isValidAgentName("1abc")).toBe(false);
    expect(isValidAgentName("Claude")).toBe(false);
    expect(isValidAgentName("has space")).toBe(false);
    expect(isValidAgentName("a".repeat(33))).toBe(false);
  });

  it("sanitizes arbitrary labels toward a valid name", () => {
    expect(sanitizeAgentName("My Claude!")).toBe("my-claude-");
    expect(sanitizeAgentName("123kind")).toBe("kind");
    expect(isValidAgentName(sanitizeAgentName("My Claude!"))).toBe(true);
  });

  it("suggests a unique name, incrementing on collision", () => {
    expect(suggestAgentName("claude", [])).toBe("claude");
    expect(suggestAgentName("claude", ["claude"])).toBe("claude-2");
    expect(suggestAgentName("claude", ["claude", "claude-2"])).toBe("claude-3");
  });
});
