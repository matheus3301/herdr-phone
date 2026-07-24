import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * Theme contrast guard.
 *
 * Parses the real token values out of src/index.css for both the dark (@theme)
 * and light (.light) palettes and asserts WCAG contrast for the pairings that
 * actually ship, so a future token edit that breaks readability fails here
 * rather than in a screenshot review.
 */
function readCss(): string {
  // The worker cwd may be web/ or the repo root.
  for (const candidate of ["src/index.css", "web/src/index.css"]) {
    try {
      const css = readFileSync(resolve(process.cwd(), candidate), "utf8");
      if (css.includes("@theme")) return css;
    } catch {
      /* try the next candidate */
    }
  }
  throw new Error(`could not locate src/index.css from ${process.cwd()}`);
}
const css = readCss();

function block(name: string): string {
  const re = new RegExp(name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\s*\\{");
  const match = re.exec(css);
  if (!match) throw new Error(`missing block ${name}`);
  const open = css.indexOf("{", match.index);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

function tokens(body: string): Record<string, string> {
  const out: Record<string, string> = {};
  const re = /--color-([\w-]+):\s*(#[0-9a-fA-F]{3,8})\s*;/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(body))) out[m[1]] = m[2];
  return out;
}

const dark = tokens(block("@theme"));
const light = { ...dark, ...tokens(block(".light")) };

function srgbToLin(c: number): number {
  const s = c / 255;
  return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}
function luminance(hex: string): number {
  const h = hex.replace("#", "");
  return (
    0.2126 * srgbToLin(parseInt(h.slice(0, 2), 16)) +
    0.7152 * srgbToLin(parseInt(h.slice(2, 4), 16)) +
    0.0722 * srgbToLin(parseInt(h.slice(4, 6), 16))
  );
}
function contrast(fg: string, bg: string): number {
  const a = luminance(fg);
  const b = luminance(bg);
  const [hi, lo] = a > b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

const REQUIRED = [
  "deck",
  "bulkhead",
  "hull",
  "seam",
  "frame",
  "mist",
  "muted-ink",
  "faint-ink",
  "brass",
  "tide",
  "flare",
  "onaccent",
  "runline",
  "terminal",
  "terminal-ink",
];

describe.each([
  ["dark", dark],
  ["light", light],
])("Dispatch Log contrast — %s", (_name, palette) => {
  it("defines every semantic token", () => {
    for (const token of REQUIRED) expect(palette[token], `missing --color-${token}`).toBeTruthy();
  });

  it("keeps the console surface dark in both themes", () => {
    // xterm's foreground is a fixed pale value; a pale console surface would
    // make it unreadable, so --color-terminal is intentionally not themed.
    expect(luminance(palette.terminal)).toBeLessThan(0.1);
    expect(contrast(palette["terminal-ink"], palette.terminal)).toBeGreaterThanOrEqual(4.5);
  });

  it("primary text is AA-readable on every surface", () => {
    for (const surface of [palette.deck, palette.bulkhead, palette.hull]) {
      expect(contrast(palette.mist, surface)).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("secondary text is AA-readable on every surface", () => {
    for (const surface of [palette.deck, palette.bulkhead, palette.hull]) {
      expect(contrast(palette["muted-ink"], surface)).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("tertiary text (ids, timestamps) clears large-text AA on every surface", () => {
    for (const surface of [palette.deck, palette.bulkhead, palette.hull]) {
      expect(contrast(palette["faint-ink"], surface)).toBeGreaterThanOrEqual(3.5);
    }
  });

  it("solid accent buttons keep readable ink", () => {
    for (const accent of [palette.brass, palette.tide, palette.flare]) {
      expect(contrast(palette.onaccent, accent)).toBeGreaterThanOrEqual(4.0);
    }
  });

  it("status text carries meaning on its own, so it must be readable as text", () => {
    for (const surface of [palette.deck, palette.bulkhead, palette.hull]) {
      expect(contrast(palette.brass, surface)).toBeGreaterThanOrEqual(4.0);
      expect(contrast(palette.tide, surface)).toBeGreaterThanOrEqual(4.0);
      expect(contrast(palette.flare, surface)).toBeGreaterThanOrEqual(4.0);
    }
  });

  it("the runline rail is visible against the app background without shouting", () => {
    const ratio = contrast(palette.runline, palette.deck);
    expect(ratio).toBeGreaterThanOrEqual(1.4);
    expect(ratio).toBeLessThan(7);
  });

  it("separators are visible but quieter than deliberate borders", () => {
    expect(contrast(palette.seam, palette.deck)).toBeGreaterThan(1.05);
    expect(contrast(palette.frame, palette.deck)).toBeGreaterThan(contrast(palette.seam, palette.deck));
  });
});

describe("the two themes are genuinely distinct", () => {
  it("flips surfaces pale and ink dark, preserving depth order", () => {
    expect(luminance(dark.deck)).toBeLessThan(0.1);
    expect(luminance(light.deck)).toBeGreaterThan(0.6);
    expect(luminance(dark.mist)).toBeGreaterThan(0.6);
    expect(luminance(light.mist)).toBeLessThan(0.1);
    // A raised surface stays lighter than the base in both themes.
    expect(luminance(dark.bulkhead)).toBeGreaterThan(luminance(dark.deck));
    expect(luminance(light.bulkhead)).toBeGreaterThan(luminance(light.deck));
    // A well stays darker than the base in both themes.
    expect(luminance(dark.hull)).toBeLessThan(luminance(dark.deck));
    expect(luminance(light.hull)).toBeLessThan(luminance(light.deck));
  });
});
