import { describe, it, expect } from "vitest";
import { encodeChord, encodeChordBytes } from "./key-encode";

describe("key encoding to terminal bytes", () => {
  it("encodes simple keys", () => {
    expect(encodeChord("enter")).toBe("\r");
    expect(encodeChord("tab")).toBe("\t");
    expect(encodeChord("esc")).toBe("\x1b");
    expect(encodeChord("backspace")).toBe("\x7f");
  });

  it("encodes arrows and modified arrows", () => {
    expect(encodeChord("up")).toBe("\x1b[A");
    expect(encodeChord("ctrl+left")).toBe("\x1b[1;5D");
    expect(encodeChord("alt+up")).toBe("\x1b[1;3A");
  });

  it("encodes shift+tab as CSI Z", () => {
    expect(encodeChord("tab")).toBe("\t");
    expect(encodeChord("shift+tab")).toBe("\x1b[Z");
  });

  it("encodes ctrl+letter as a control code", () => {
    expect(encodeChord("ctrl+c")).toBe("\x03");
    expect(encodeChord("ctrl+d")).toBe("\x04");
  });

  it("encodes alt+char with an ESC prefix", () => {
    expect(encodeChord("alt+f")).toBe("\x1bf");
  });

  it("returns bytes and refuses unencodable chords", () => {
    const bytes = encodeChordBytes("enter");
    expect(bytes && ArrayBuffer.isView(bytes)).toBe(true);
    expect(Array.from(bytes ?? [])).toEqual([13]);
    expect(encodeChord("ctrl+1")).toBeNull();
    expect(encodeChordBytes("ctrl+1")).toBeNull();
  });
});
