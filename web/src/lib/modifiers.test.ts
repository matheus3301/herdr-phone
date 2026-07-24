import { describe, it, expect } from "vitest";
import { cycle, cycleModifier, afterKey, armed, emptyModifiers } from "./modifiers";

describe("tri-state modifiers", () => {
  it("cycles off -> next -> locked -> off", () => {
    expect(cycle("off")).toBe("next");
    expect(cycle("next")).toBe("locked");
    expect(cycle("locked")).toBe("off");
  });

  it("cycleModifier only advances the named modifier", () => {
    const m = cycleModifier(emptyModifiers(), "ctrl");
    expect(m.ctrl).toBe("next");
    expect(m.alt).toBe("off");
    expect(m.shift).toBe("off");
  });

  it("armed lists any non-off modifier", () => {
    const m = { ctrl: "locked", alt: "next", shift: "off" } as const;
    expect(armed(m)).toEqual(["ctrl", "alt"]);
  });

  it("afterKey clears one-shot 'next' but keeps 'locked'", () => {
    const m = { ctrl: "locked", alt: "next", shift: "off" } as const;
    const out = afterKey(m);
    expect(out.ctrl).toBe("locked");
    expect(out.alt).toBe("off");
    expect(out.shift).toBe("off");
  });
});
