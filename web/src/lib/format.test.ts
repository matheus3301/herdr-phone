import { describe, it, expect } from "vitest";
import { statusLabel, statusTone, relativeTime, shortPath, stripControl } from "./format";

describe("format helpers", () => {
  it("labels statuses for humans", () => {
    expect(statusLabel("blocked")).toBe("Needs you");
    expect(statusLabel("working")).toBe("Working");
  });

  it("maps statuses to palette tones", () => {
    expect(statusTone("blocked")).toBe("flare");
    expect(statusTone("working")).toBe("tide");
    expect(statusTone("done")).toBe("brass");
    expect(statusTone("idle")).toBe("muted");
  });

  it("formats relative time", () => {
    const now = 1_000_000;
    expect(relativeTime(now, now)).toBe("just now");
    expect(relativeTime(now - 30_000, now)).toBe("30s ago");
    expect(relativeTime(now - 5 * 60_000, now)).toBe("5m ago");
    expect(relativeTime(now - 3 * 3600_000, now)).toBe("3h ago");
    expect(relativeTime(now - 2 * 86_400_000, now)).toBe("2d ago");
  });

  it("shortens home paths and long paths", () => {
    expect(shortPath("/Users/dev/code/space-api")).toBe(".../code/space-api");
    expect(shortPath("/Users/dev")).toBe("~");
  });

  it("strips control characters", () => {
    expect(stripControl("a\x1b[31mb\x07c")).toBe("a[31mbc");
  });
});
