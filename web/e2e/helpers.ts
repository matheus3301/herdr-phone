import { type Page, expect } from "@playwright/test";

/** Pair a device via the fragment handoff and wait for the app shell. Each paired
 * page gets its own in-memory session + CSRF token (mutations need it). When
 * `reset` is true (default) the shared mock herd is reset first for test
 * isolation; pass false to pair a *second* controller without disturbing state. */
export async function pair(page: Page, opts: { reset?: boolean } = {}) {
  if (opts.reset ?? true) await page.request.post("/api/v1/__reset");
  await page.goto("/#pair=dev-pair-secret");
  await expect(page.getByTestId("terminal-host")).toBeVisible({ timeout: 20_000 });
  // Fragment must be stripped from the URL (SPEC §9.1).
  await expect.poll(() => page.evaluate(() => window.location.hash)).toBe("");
}

/** Navigate the bottom/side nav to a primary section. */
export async function goToSection(page: Page, name: "Terminal" | "Herd" | "Spaces") {
  await page.getByRole("link", { name }).first().click();
}
