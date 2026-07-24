import { test, expect, type Page, type Locator } from "@playwright/test";
import { pair, goToSection } from "./helpers";

/** Set the persisted theme before the app loads (prefsStore reads it on boot). */
async function setTheme(page: Page, theme: "light" | "dark") {
  await page.addInitScript((t) => {
    localStorage.setItem("herdr-phone.prefs", JSON.stringify({ theme: t, terminalFontSize: 13 }));
  }, theme);
}

/** Compute the WCAG contrast ratio of an element's text against its rendered
 * background (walking up to the first opaque ancestor) — the real, post-cascade
 * colors, so a mixed-theme regression fails here. */
async function contrastOf(page: Page, target: Locator): Promise<number> {
  const handle = await target.first().elementHandle();
  if (!handle) throw new Error("element not found for contrast check");
  return page.evaluate((el) => {
    const parse = (s: string): { rgb: [number, number, number]; a: number } | null => {
      const m = s.match(/rgba?\(([^)]+)\)/);
      if (!m) return null;
      const p = m[1].split(",").map((x) => parseFloat(x));
      return { rgb: [p[0], p[1], p[2]], a: p[3] === undefined ? 1 : p[3] };
    };
    const lum = ([r, g, b]: [number, number, number]) => {
      const f = (c: number) => {
        const s = c / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const fg = parse(getComputedStyle(el as Element).color)?.rgb ?? [0, 0, 0];
    // Collect background layers from the element up to the first opaque one, then
    // alpha-composite them so a translucent tint (e.g. bg-flare/10) resolves to
    // its real rendered color rather than being mistaken for an opaque fill.
    const layers: Array<{ rgb: [number, number, number]; a: number }> = [];
    let node: Element | null = el as Element;
    while (node) {
      const c = parse(getComputedStyle(node).backgroundColor);
      if (c && c.a > 0) {
        layers.push(c);
        if (c.a >= 1) break;
      }
      node = node.parentElement;
    }
    let bg: [number, number, number] = layers.length ? layers[layers.length - 1].rgb : [255, 255, 255];
    for (let i = layers.length - 2; i >= 0; i--) {
      const t = layers[i];
      bg = [0, 1, 2].map((k) => t.rgb[k] * t.a + bg[k] * (1 - t.a)) as [number, number, number];
    }
    const l1 = lum(fg);
    const l2 = lum(bg);
    const hi = Math.max(l1, l2);
    const lo = Math.min(l1, l2);
    return (hi + 0.05) / (lo + 0.05);
  }, handle);
}

/** Luminance of the effective (alpha-composited) background behind an element. */
async function bgLuminance(page: Page, target: Locator): Promise<number> {
  const handle = await target.first().elementHandle();
  if (!handle) throw new Error("element not found for bg check");
  return page.evaluate((el) => {
    const parse = (s: string): { rgb: [number, number, number]; a: number } | null => {
      const m = s.match(/rgba?\(([^)]+)\)/);
      if (!m) return null;
      const p = m[1].split(",").map((x) => parseFloat(x));
      return { rgb: [p[0], p[1], p[2]], a: p[3] === undefined ? 1 : p[3] };
    };
    const lum = ([r, g, b]: [number, number, number]) => {
      const f = (c: number) => {
        const s = c / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const layers: Array<{ rgb: [number, number, number]; a: number }> = [];
    let node: Element | null = el as Element;
    while (node) {
      const c = parse(getComputedStyle(node).backgroundColor);
      if (c && c.a > 0) {
        layers.push(c);
        if (c.a >= 1) break;
      }
      node = node.parentElement;
    }
    let bg: [number, number, number] = layers.length ? layers[layers.length - 1].rgb : [255, 255, 255];
    for (let i = layers.length - 2; i >= 0; i--) {
      const t = layers[i];
      bg = [0, 1, 2].map((k) => t.rgb[k] * t.a + bg[k] * (1 - t.a)) as [number, number, number];
    }
    return lum(bg);
  }, handle);
}

for (const theme of ["light", "dark"] as const) {
  test.describe(`${theme} theme readability`, () => {
    test(`Herd, action buttons, and dialogs are readable (${theme})`, async ({ page }) => {
      await setTheme(page, theme);
      await pair(page);
      await expect
        .poll(() => page.evaluate(() => document.documentElement.classList.contains("light")))
        .toBe(theme === "light");

      // The terminal surface stays dark in BOTH themes (xterm's pale foreground
      // must never sit on a pale surface).
      const termBgLum = await bgLuminance(page, page.getByTestId("terminal-host"));
      expect(termBgLum).toBeLessThan(0.15);

      // Herd: a blocked agent's name and its question must be readable on the card.
      await goToSection(page, "Herd");
      await expect(page.getByText(/Approve this command\?/)).toBeVisible();
      expect(await contrastOf(page, page.getByText("claude").first())).toBeGreaterThanOrEqual(4.5);
      expect(await contrastOf(page, page.getByText(/Approve this command\?/))).toBeGreaterThanOrEqual(4.5);
      // Primary enabled action label ("Open terminal") on its accent background.
      expect(await contrastOf(page, page.getByRole("button", { name: /open terminal/i }).first())).toBeGreaterThanOrEqual(4.0);

      // Spaces: the primary "New workspace" action.
      await goToSection(page, "Spaces");
      const newWs = page.getByRole("button", { name: /new workspace/i });
      await expect(newWs).toBeVisible();
      expect(await contrastOf(page, newWs)).toBeGreaterThanOrEqual(4.0);

      // A dialog: create-tab sheet title + primary action.
      await goToSection(page, "Terminal");
      await page.getByRole("button", { name: /open tab switcher/i }).click();
      await page.getByRole("button", { name: /new tab/i }).click();
      await expect(page.getByRole("dialog")).toBeVisible();
      expect(await contrastOf(page, page.getByRole("button", { name: /create tab/i }))).toBeGreaterThanOrEqual(4.0);
    });
  });
}
