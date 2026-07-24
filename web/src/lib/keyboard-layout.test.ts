import { describe, it, expect } from "vitest";
import { keyboardInset, keyboardOpen } from "./keyboard-layout";

describe("keyboard layout math", () => {
  it("reports zero inset when the visual viewport fills the layout", () => {
    expect(keyboardInset({ layoutHeight: 844, visualHeight: 844, offsetTop: 0 })).toBe(0);
    expect(keyboardOpen({ layoutHeight: 844, visualHeight: 844, offsetTop: 0 })).toBe(false);
  });

  it("computes the keyboard overlap when the visual viewport shrinks", () => {
    // 844 layout, keyboard hides 300px at the bottom.
    expect(keyboardInset({ layoutHeight: 844, visualHeight: 544, offsetTop: 0 })).toBe(300);
    expect(keyboardOpen({ layoutHeight: 844, visualHeight: 544, offsetTop: 0 })).toBe(true);
  });

  it("ignores tiny overlaps that are browser chrome, not a keyboard", () => {
    expect(keyboardInset({ layoutHeight: 844, visualHeight: 830, offsetTop: 0 })).toBe(0);
  });

  it("accounts for a visual viewport offset from the top", () => {
    expect(keyboardInset({ layoutHeight: 844, visualHeight: 500, offsetTop: 44 })).toBe(300);
  });
});
