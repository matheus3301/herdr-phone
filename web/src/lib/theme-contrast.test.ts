import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// Read the stylesheet source directly (single source of truth for token values).
// The worker cwd may be the web/ root or the repo root, so try both.
function readCss(): string {
  for (const p of ["src/index.css", "web/src/index.css"]) {
    try {
      const c = readFileSync(resolve(process.cwd(), p), "utf8");
      if (c.includes("@theme")) return c;
    } catch {
      /* try next candidate */
    }
  }
  throw new Error(`could not locate src/index.css from ${process.cwd()}`);
}
const css = readCss();

/**
 * Theme contrast guard. Parses the real token values from src/index.css for both
 * the dark (@theme) and light (.light) palettes and asserts WCAG contrast for the
 * pairs that actually appear in the UI, so a future token edit that breaks
 * readability (the mixed-theme bug this fixes) fails CI.
 */

function block(name: string): string {
  // Anchor on the selector's own opening brace so a mention of the selector
  // elsewhere (e.g. ".light" inside @custom-variant) is not matched.
  const re = new RegExp(name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\s*\\{");
  const m = re.exec(css);
  if (!m) throw new Error(`missing block ${name}`);
  const open = css.indexOf("{", m.index);
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
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return 0.2126 * srgbToLin(r) + 0.7152 * srgbToLin(g) + 0.0722 * srgbToLin(b);
}
function contrast(fg: string, bg: string): number {
  const a = luminance(fg);
  const b = luminance(bg);
  const [hi, lo] = a > b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

describe.each([
  ["dark", dark],
  ["light", light],
])("theme contrast — %s", (_name, p) => {
  it("has all named tokens defined", () => {
    for (const k of ["deck", "bulkhead", "hull", "mist", "muted-ink", "brass", "tide", "flare", "onaccent", "frame", "terminal"]) {
      expect(p[k], `missing --color-${k}`).toBeTruthy();
    }
  });

  it("keeps the terminal surface dark in both themes (xterm foreground is pale)", () => {
    expect(luminance(p.terminal)).toBeLessThan(0.1);
    // xterm's fixed pale foreground (#dce7e4) must stay readable on it.
    expect(contrast("#dce7e4", p.terminal)).toBeGreaterThanOrEqual(4.5);
  });

  it("primary text (Mist) is AA-readable on every surface", () => {
    expect(contrast(p.mist, p.deck)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(p.mist, p.bulkhead)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(p.mist, p.hull)).toBeGreaterThanOrEqual(4.5);
  });

  it("secondary ink (muted) is readable on the base surface", () => {
    expect(contrast(p["muted-ink"], p.deck)).toBeGreaterThanOrEqual(4.0);
    expect(contrast(p["muted-ink"], p.hull)).toBeGreaterThanOrEqual(4.0);
  });

  it("solid accent buttons: on-accent ink is AA-readable on Brass/Tide/Flare", () => {
    expect(contrast(p.onaccent, p.brass)).toBeGreaterThanOrEqual(4.0);
    expect(contrast(p.onaccent, p.tide)).toBeGreaterThanOrEqual(4.0);
    expect(contrast(p.onaccent, p.flare)).toBeGreaterThanOrEqual(4.0);
  });

  it("accent text (Brass/Tide/Flare) has accessible contrast on surfaces", () => {
    for (const surface of [p.deck, p.bulkhead, p.hull]) {
      expect(contrast(p.brass, surface)).toBeGreaterThanOrEqual(3.0);
      expect(contrast(p.tide, surface)).toBeGreaterThanOrEqual(3.0);
      expect(contrast(p.flare, surface)).toBeGreaterThanOrEqual(3.0);
    }
  });
});

describe("themes are actually distinct (not mixed)", () => {
  it("surfaces flip pale and ink flips dark under .light", () => {
    // Deck: dark surface in dark mode, pale surface in light mode.
    expect(luminance(dark.deck)).toBeLessThan(0.1);
    expect(luminance(light.deck)).toBeGreaterThan(0.6);
    // Mist ink: pale in dark mode, dark in light mode.
    expect(luminance(dark.mist)).toBeGreaterThan(0.6);
    expect(luminance(light.mist)).toBeLessThan(0.1);
    // The raised surface stays lighter than the base in both themes (depth cue).
    expect(luminance(dark.bulkhead)).toBeGreaterThan(luminance(dark.deck));
    expect(luminance(light.bulkhead)).toBeGreaterThan(luminance(light.deck));
  });
});
