import { describe, it, expect } from "vitest";
import { composeChord, isValidBaseKey, isValidChord, isDangerChord, chordCaption } from "./keys";
import { emptyModifiers } from "./modifiers";

describe("logical keys", () => {
  it("validates base keys, function keys, and single chars", () => {
    expect(isValidBaseKey("enter")).toBe(true);
    expect(isValidBaseKey("F12")).toBe(true);
    expect(isValidBaseKey("a")).toBe(true);
    expect(isValidBaseKey("nope")).toBe(false);
  });

  it("composes chords in canonical ctrl+alt+shift order", () => {
    const mods = { ctrl: "next", alt: "off", shift: "locked" } as const;
    expect(composeChord(mods, "P")).toBe("ctrl+shift+p");
  });

  it("composes a bare key with no modifiers", () => {
    expect(composeChord(emptyModifiers(), "enter")).toBe("enter");
  });

  it("validates composed chords and rejects duplicates/unknown mods", () => {
    expect(isValidChord("ctrl+shift+p")).toBe(true);
    expect(isValidChord("ctrl+ctrl+p")).toBe(false);
    expect(isValidChord("hyper+p")).toBe(false);
    expect(isValidChord("ctrl+nope")).toBe(false);
  });

  it("flags interrupt/EOF/suspend danger keys", () => {
    expect(isDangerChord("ctrl+c")).toBe(true);
    expect(isDangerChord("ctrl+d")).toBe(true);
    expect(isDangerChord("ctrl+a")).toBe(false);
  });

  it("captions chords readably", () => {
    expect(chordCaption("ctrl+c")).toBe("Ctrl+C");
    expect(chordCaption("enter")).toBe("Enter");
  });
});
