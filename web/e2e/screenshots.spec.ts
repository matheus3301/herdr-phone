import { test, type Page } from "@playwright/test";
import { pair, goToSection } from "./helpers";

// Screenshot review sizes (SPEC §18): 390x844, 430x932, 768x1024, 1440x900.
// Runs once (desktop/chromium project) and writes PNGs to e2e/__screenshots__.
const SIZES = [
  { name: "iphone-390x844", width: 390, height: 844 },
  { name: "iphone-430x932", width: 430, height: 932 },
  { name: "tablet-768x1024", width: 768, height: 1024 },
  { name: "desktop-1440x900", width: 1440, height: 900 },
];

// Light captures at the two key widths the QA pass targets (390 + desktop).
const LIGHT_SIZES = [
  { name: "light-390x844", width: 390, height: 844 },
  { name: "light-1440x900", width: 1440, height: 900 },
];

async function captureCore(page: Page, suffix: string) {
  await page.screenshot({ path: `e2e/__screenshots__/terminal-${suffix}.png`, fullPage: false });
  await goToSection(page, "Herd");
  await page.getByRole("heading", { name: /needs you/i }).first().waitFor();
  await page.screenshot({ path: `e2e/__screenshots__/herd-${suffix}.png`, fullPage: false });
  await goToSection(page, "Spaces");
  await page.getByRole("button", { name: /new workspace/i }).waitFor();
  await page.screenshot({ path: `e2e/__screenshots__/spaces-${suffix}.png`, fullPage: false });
}

test.describe("screenshot review", () => {
  test.skip(({ browserName }) => browserName !== "chromium", "capture once");

  test("capture core screens at review sizes (dark)", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "desktop", "capture once, from the desktop project");
    test.setTimeout(120_000);
    for (const size of SIZES) {
      await page.setViewportSize({ width: size.width, height: size.height });
      await pair(page);
      await captureCore(page, size.name);
    }
  });

  test("capture core screens in light mode (390 + desktop)", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "desktop", "capture once, from the desktop project");
    test.setTimeout(120_000);
    await page.addInitScript(() => {
      localStorage.setItem("herdr-phone.prefs", JSON.stringify({ theme: "light", terminalFontSize: 13 }));
    });
    for (const size of LIGHT_SIZES) {
      await page.setViewportSize({ width: size.width, height: size.height });
      await pair(page);
      await captureCore(page, size.name);
    }
  });
});
