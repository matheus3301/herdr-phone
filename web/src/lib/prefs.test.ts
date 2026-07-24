import { describe, it, expect, beforeEach } from "vitest";
import { prefsStore, effectiveTheme, applyTheme } from "./prefs";

describe("prefs store", () => {
  beforeEach(() => {
    localStorage.clear();
    prefsStore.set({ theme: "system", terminalFontSize: 13 });
  });

  it("persists updates to localStorage", () => {
    prefsStore.set({ terminalFontSize: 16 });
    expect(prefsStore.get().terminalFontSize).toBe(16);
    expect(localStorage.getItem("herdr-phone.prefs")).toContain("16");
  });

  it("notifies subscribers", () => {
    let calls = 0;
    const off = prefsStore.subscribe(() => calls++);
    prefsStore.set({ theme: "dark" });
    off();
    expect(calls).toBe(1);
  });

  it("resolves system theme via matchMedia (defaults dark)", () => {
    expect(effectiveTheme("dark")).toBe("dark");
    expect(effectiveTheme("light")).toBe("light");
    expect(effectiveTheme("system")).toBe("dark");
  });

  it("applies the theme class to the document element", () => {
    applyTheme("light");
    expect(document.documentElement.classList.contains("light")).toBe(true);
    applyTheme("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(document.documentElement.classList.contains("light")).toBe(false);
  });
});
