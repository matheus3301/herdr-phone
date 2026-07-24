import { test, expect, type Page } from "@playwright/test";
import { goTo, openRun, openWorkspace, pair, presetTheme } from "./helpers";

/**
 * Screenshot review. Captures the production bundle against the mock herd at the
 * widths the design targets, in both themes, and writes PNGs to
 * e2e/__screenshots__ for a human (or an agent) to inspect.
 */
const SIZES = [
  { name: "iphone-390x844", width: 390, height: 844 },
  { name: "iphone-430x932", width: 430, height: 932 },
  { name: "tablet-768x1024", width: 768, height: 1024 },
  { name: "desktop-1440x900", width: 1440, height: 900 },
];

const LIGHT_SIZES = [
  { name: "light-390x844", width: 390, height: 844 },
  { name: "light-768x1024", width: 768, height: 1024 },
  { name: "light-1440x900", width: 1440, height: 900 },
];

async function captureAll(page: Page, suffix: string, theme: "light" | "dark") {
  // Guard the capture itself: a screenshot review is worthless if the theme it
  // claims to show is not the one that rendered.
  await expect
    .poll(() => page.evaluate(() => document.documentElement.classList.contains("light")))
    .toBe(theme === "light");
  await page.screenshot({ path: `e2e/__screenshots__/agents-${suffix}.png` });

  await openRun(page, "claude");
  await expect(page.getByRole("heading", { name: /observed activity/i })).toBeVisible();
  // Give the bounded pane read time to land so the fallback is visible.
  await expect(page.getByText(/recent terminal output/i)).toBeVisible();
  await page.screenshot({ path: `e2e/__screenshots__/run-${suffix}.png` });

  await goTo(page, "Start run");
  await expect(page.getByRole("heading", { level: 1, name: "Start run" })).toBeVisible();
  await page.screenshot({ path: `e2e/__screenshots__/start-run-${suffix}.png` });

  await goTo(page, "Workspaces");
  await expect(page.getByRole("heading", { level: 1, name: "Workspaces" })).toBeVisible();
  await page.screenshot({ path: `e2e/__screenshots__/workspaces-${suffix}.png` });

  await openWorkspace(page, "space-api");
  await page.screenshot({ path: `e2e/__screenshots__/workspace-detail-${suffix}.png` });
}

test.describe("screenshot review", () => {
  test.skip(({ browserName }) => browserName !== "chromium", "capture once");

  test("capture the product at review sizes (dark)", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "desktop", "capture once, from the desktop project");
    test.setTimeout(180_000);
    // Explicit: the app's default theme follows the OS, and a headless Chromium
    // reports a light preference.
    await presetTheme(page, "dark");
    for (const size of SIZES) {
      await page.setViewportSize({ width: size.width, height: size.height });
      await pair(page);
      await captureAll(page, size.name, "dark");
    }
  });

  test("capture the product at review sizes (light)", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "desktop", "capture once, from the desktop project");
    test.setTimeout(180_000);
    await presetTheme(page, "light");
    for (const size of LIGHT_SIZES) {
      await page.setViewportSize({ width: size.width, height: size.height });
      await pair(page);
      await captureAll(page, size.name, "light");
    }
  });

  test("capture the console", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "desktop", "capture once, from the desktop project");
    test.setTimeout(120_000);
    await presetTheme(page, "dark");
    for (const size of [SIZES[0], SIZES[3]]) {
      await page.setViewportSize({ width: size.width, height: size.height });
      await pair(page);
      await page.goto("/console/w1%3Ap1?generation=3");
      await expect(page.getByTestId("terminal-host")).toContainText("herdr", { timeout: 20_000 });
      await page.screenshot({ path: `e2e/__screenshots__/console-${size.name}.png` });
    }
  });
});
