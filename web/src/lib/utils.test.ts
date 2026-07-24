import { describe, it, expect } from "vitest";
import { cn, clamp, uuid } from "./utils";
import { buttonVariants } from "@/components/ui/button-variants";

describe("cn — the type scale and the palette are different groups", () => {
  it("keeps a custom text size and a custom text colour together", () => {
    // Before the merge config knew the type scale, the size deleted the colour
    // and a solid button rendered its label in the inherited ink.
    const result = cn("text-body", "text-onaccent");
    expect(result).toContain("text-body");
    expect(result).toContain("text-onaccent");
  });

  it("still resolves a genuine conflict within one group", () => {
    expect(cn("text-body", "text-meta")).toBe("text-meta");
    expect(cn("text-mist", "text-flare")).toBe("text-flare");
  });

  it("every solid button variant keeps its on-accent ink at every size", () => {
    for (const variant of ["primary", "ok", "danger"] as const) {
      for (const size of ["default", "sm", "lg", "icon", "chip", "key"] as const) {
        expect(cn(buttonVariants({ variant, size })), `${variant}/${size}`).toContain("text-onaccent");
      }
    }
  });

  it("resolves ordinary Tailwind conflicts as usual", () => {
    expect(cn("px-2", "px-4")).toBe("px-4");
    const active = false;
    expect(cn("hidden", active && "block", undefined)).toBe("hidden");
  });
});

describe("uuid", () => {
  it("produces a distinct, correctly shaped id", () => {
    const a = uuid();
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    expect(uuid()).not.toBe(a);
  });
});

describe("clamp", () => {
  it("bounds a value", () => {
    expect(clamp(5, 1, 3)).toBe(3);
    expect(clamp(0, 1, 3)).toBe(1);
    expect(clamp(2, 1, 3)).toBe(2);
  });
});
