import { describe, it, expect } from "vitest";
import { sanitizePaste } from "./paste";

describe("sanitizePaste (F1)", () => {
  it("preserves multi-line and tab structure", () => {
    expect(sanitizePaste("echo a\necho b")).toBe("echo a\necho b");
    expect(sanitizePaste("col1\tcol2")).toBe("col1\tcol2");
    expect(sanitizePaste("a\nb\tc\nd")).toBe("a\nb\tc\nd");
  });

  it("normalizes CRLF and lone CR to LF (never concatenates lines)", () => {
    expect(sanitizePaste("echo a\r\necho b")).toBe("echo a\necho b");
    expect(sanitizePaste("echo a\recho b")).toBe("echo a\necho b");
  });

  it("strips ESC and other C0/C1 controls but keeps the text", () => {
    expect(sanitizePaste("echo\x1b[31m hi")).toBe("echo[31m hi");
    expect(sanitizePaste("a\x00\x07\x1bb")).toBe("ab");
    expect(sanitizePaste("keep\x9cme")).toBe("keepme");
  });

  it("cannot smuggle a bracketed-paste end marker (ESC removed)", () => {
    // The ESC in a fake end marker is stripped, so the interior can't break out.
    expect(sanitizePaste("payload\x1b[201~rm -rf /")).toBe("payload[201~rm -rf /");
  });

  it("returns empty for whitespace-only control input", () => {
    expect(sanitizePaste("\x00\x1b")).toBe("");
  });
});
