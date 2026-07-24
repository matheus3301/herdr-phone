import { test, expect, type Locator, type Page } from "@playwright/test";
import { goTo, inbox, instructions, main, openRun, pair, presetTheme } from "./helpers";

/**
 * Contrast of an element's text against its *rendered* background — alpha layers
 * composited up to the first opaque ancestor — so a mixed-theme regression fails
 * here rather than in a screenshot review.
 */
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

async function backgroundLuminance(page: Page, target: Locator): Promise<number> {
  const handle = await target.first().elementHandle();
  if (!handle) throw new Error("element not found for background check");
  return page.evaluate((el) => {
    const parse = (s: string) => {
      const m = s.match(/rgba?\(([^)]+)\)/);
      if (!m) return null;
      const p = m[1].split(",").map((x) => parseFloat(x));
      return { rgb: [p[0], p[1], p[2]] as [number, number, number], a: p[3] === undefined ? 1 : p[3] };
    };
    const lum = ([r, g, b]: [number, number, number]) => {
      const f = (c: number) => {
        const s = c / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    let node: Element | null = el as Element;
    while (node) {
      const c = parse(getComputedStyle(node).backgroundColor);
      if (c && c.a >= 1) return lum(c.rgb);
      node = node.parentElement;
    }
    return 1;
  }, handle);
}

for (const theme of ["light", "dark"] as const) {
  test.describe(`${theme} theme readability`, () => {
    test(`the inbox, a run, and its controls are readable (${theme})`, async ({ page }) => {
      await presetTheme(page, theme);
      await pair(page);
      await expect
        .poll(() => page.evaluate(() => document.documentElement.classList.contains("light")))
        .toBe(theme === "light");

      // Inbox: section heading, agent name, Herdr's pane title, status word.
      expect(await contrastOf(page, inbox(page).getByRole("heading", { level: 2, name: /needs you/i }))).toBeGreaterThanOrEqual(4.0);
      expect(await contrastOf(page, inbox(page).getByText("Approve this command?"))).toBeGreaterThanOrEqual(4.5);
      expect(await contrastOf(page, inbox(page).getByText("Needs you").nth(1))).toBeGreaterThanOrEqual(4.0);

      // The one deliberate primary action.
      expect(await contrastOf(page, inbox(page).getByRole("link", { name: "Start run" }))).toBeGreaterThanOrEqual(4.0);

      // Run detail: prose, the instruction block, and the composer.
      await openRun(page, "claude");
      expect(await contrastOf(page, main(page).getByText(/a decision is required/i))).toBeGreaterThanOrEqual(4.5);
      await page.getByLabel("Instruction for claude").fill("continue");
      await page.getByRole("button", { name: "Send instruction" }).click();
      await expect(instructions(page).getByText("Delivered")).toBeVisible();
      expect(await contrastOf(page, instructions(page).getByText("continue").first())).toBeGreaterThanOrEqual(4.5);

      // The console stays a dark instrument in both themes.
      await main(page).getByRole("link", { name: /open console/i }).first().click();
      await expect(page.getByTestId("terminal-host")).toBeVisible({ timeout: 20_000 });
      expect(await backgroundLuminance(page, page.getByTestId("terminal-host"))).toBeLessThan(0.15);
    });

    test(`workspaces and dialogs are readable (${theme})`, async ({ page }) => {
      await presetTheme(page, theme);
      await pair(page);
      await goTo(page, "Workspaces");
      expect(await contrastOf(page, main(page).getByText("space-api").first())).toBeGreaterThanOrEqual(4.5);
      expect(await contrastOf(page, main(page).getByRole("button", { name: /^new$/i }))).toBeGreaterThanOrEqual(4.0);

      await main(page).getByRole("button", { name: /^new$/i }).click();
      await expect(page.getByRole("dialog")).toBeVisible();
      expect(await contrastOf(page, page.getByRole("button", { name: /create workspace/i }))).toBeGreaterThanOrEqual(4.0);
    });
  });
}
