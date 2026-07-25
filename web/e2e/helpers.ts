import { expect, type Page } from "@playwright/test";

/**
 * Pair a device via the fragment handoff and wait for the agent inbox.
 *
 * Each paired page gets its own in-memory session and CSRF token. When `reset`
 * is true (the default) the shared mock herd is reset first for isolation; pass
 * false to pair a *second* controller without disturbing state.
 */
export async function pair(page: Page, opts: { reset?: boolean } = {}) {
  if (opts.reset ?? true) await page.request.post("/api/v1/__reset");
  await page.goto("/#pair=dev-pair-secret");
  await expect(page.getByRole("heading", { level: 1, name: "Agents" })).toBeVisible({ timeout: 20_000 });
  // The pairing fragment must never be left in the URL or history.
  await expect.poll(() => page.evaluate(() => window.location.hash)).toBe("");
}

/** The inbox column. Always mounted; the only column at phone width. */
export function inbox(page: Page) {
  return page.locator("aside.shell-inbox");
}

/** Open a run from the inbox by the agent's name. */
export async function openRun(page: Page, agentName: string) {
  await inbox(page).getByRole("link", { name: new RegExp(`\\b${agentName}\\b`) }).first().click();
  await expect(page.getByRole("heading", { level: 1, name: agentName })).toBeVisible();
}

/** The "Your instructions" section of an open run. */
export function instructions(page: Page) {
  return page.locator("section[aria-labelledby='instructions-heading']");
}

/** Navigate to a primary destination. */
export async function goTo(page: Page, name: "Agents" | "Start run" | "Workspaces") {
  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name }).click();
}

/**
 * The detail column. At desktop width the inbox is a permanent second column, so
 * a workspace label can appear both there and in the detail view; scope to the
 * main region to stay unambiguous at every width.
 */
export function main(page: Page) {
  return page.locator("#main");
}

/** Open a workspace from the workspace list. */
export async function openWorkspace(page: Page, label: string) {
  await main(page).getByRole("link", { name: new RegExp(label) }).first().click();
  await expect(page.getByRole("heading", { level: 1, name: label })).toBeVisible();
}

/** Make the next call to `operation` fail once (test-only mock hook). */
export async function failNext(
  page: Page,
  operation: string,
  body: { status?: number; code?: string; message?: string; retryable?: boolean } = {},
) {
  await page.request.post("/api/v1/__fail_next", { data: { operation, ...body } });
}

/** Recycle a pane the way Herdr does: same id, new lifecycle generation. */
export async function replacePane(page: Page, paneId: string) {
  const res = await page.request.post("/api/v1/__replace_pane", { data: { pane_id: paneId } });
  return (await res.json()) as { pane_id: string; generation: number };
}

/**
 * Reconfigure the mock's structured run contract (test-only hook).
 *
 * `supported: false` models an OLDER relay: `/capabilities` drops the `runs`
 * document and both run routes 404, which is what the browser must fail closed
 * against.
 */
export async function setRunContract(
  page: Page,
  body: { supported?: boolean; max_runs?: number; output_padding?: number },
) {
  await page.request.post("/api/v1/__run_contract", { data: body });
}

/** Make the next observed-output read fail once with a stable relay code. */
export async function failNextRunRead(
  page: Page,
  body: { status?: number; code?: string; message?: string } = {},
) {
  await page.request.post("/api/v1/__fail_next_run_read", { data: body });
}

/** Persist a theme before the app boots (the prefs store reads it on load). */
export async function presetTheme(page: Page, theme: "light" | "dark") {
  await page.addInitScript((t) => {
    localStorage.setItem("herdr-phone.prefs", JSON.stringify({ theme: t, terminalFontSize: 13 }));
  }, theme);
}
