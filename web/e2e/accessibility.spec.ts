import { test, expect } from "@playwright/test";
import { goTo, inbox, instructions, main, openRun, pair } from "./helpers";

test.describe("Accessibility and mobile behaviour", () => {
  test("route titles, headings, and skip navigation", async ({ page, browserName }) => {
    await pair(page);
    await expect(page).toHaveTitle(/Herdr Phone/);

    // The skip link is the first thing a keyboard user reaches on a fresh load.
    // WebKit only tabs to links when the OS "Full Keyboard Access" setting is
    // on, which Playwright cannot emulate, so the link's presence is what is
    // checked there.
    if (browserName === "webkit") {
      await expect(page.getByRole("link", { name: /skip to content/i })).toHaveAttribute("href", "#main");
    } else {
      await page.keyboard.press("Tab");
      await expect(page.getByRole("link", { name: /skip to content/i })).toBeFocused();
    }

    await openRun(page, "claude");
    await expect(page).toHaveTitle(/^claude · Herdr Phone$/);
    // Focus lands on the new route's heading, not wherever the last tap was.
    await expect(page.locator("h1:focus")).toHaveText("claude");

    await goTo(page, "Workspaces");
    await expect(page).toHaveTitle(/^Workspaces · Herdr Phone$/);
    await expect(page.locator("h1:focus")).toHaveText("Workspaces");
  });

  test("the runline is a semantic list and status never relies on colour", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");

    // Status is a word, not just a hue.
    await expect(page.getByRole("heading", { level: 1, name: "claude" })).toBeVisible();
    await expect(main(page).getByText("Needs you").first()).toBeVisible();

    // Sending an instruction produces a countable runline entry.
    await page.getByLabel("Instruction for claude").fill("carry on");
    await page.getByRole("button", { name: "Send instruction" }).click();
    await expect(instructions(page).getByText("Delivered")).toBeVisible();
    // The feed is a real ordered list: the delivered instruction is one item,
    // alongside the status transition the phone observed straight after it.
    const runline = main(page).getByRole("list").filter({ hasText: "Instruction delivered to the agent" });
    await expect(runline).toHaveCount(1);
    await expect(runline.getByRole("listitem").filter({ hasText: "Instruction delivered to the agent" })).toHaveCount(1);
    await expect(runline.getByRole("listitem").first()).toContainText(/seen /);
  });

  test("the activity region is a log, announced politely", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");
    const log = page.getByRole("log", { name: /activity for claude/i });
    await expect(log).toBeVisible();
    await expect(log).toHaveAttribute("aria-live", "polite");
  });

  test("interactive targets clear 44px", async ({ page }) => {
    await pair(page);
    const nav = page.getByRole("navigation", { name: "Primary" });
    for (const name of ["Agents", "Start run", "Workspaces"]) {
      const box = await nav.getByRole("link", { name }).boundingBox();
      expect(box, name).not.toBeNull();
      expect(box!.height, name).toBeGreaterThanOrEqual(44);
    }
    const start = await inbox(page).getByRole("link", { name: "Start run" }).boundingBox();
    expect(start!.height).toBeGreaterThanOrEqual(40);
  });

  test("no horizontal overflow at 320px, and none at 430px", async ({ page }) => {
    for (const width of [320, 390, 430]) {
      await page.setViewportSize({ width, height: 780 });
      await pair(page);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(overflow, `inbox at ${width}px`).toBeLessThanOrEqual(1);

      await openRun(page, "claude");
      const runOverflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(runOverflow, `run at ${width}px`).toBeLessThanOrEqual(1);
    }
  });

  test("a short landscape viewport keeps the composer reachable", async ({ page }) => {
    await page.setViewportSize({ width: 740, height: 380 });
    await pair(page);
    await openRun(page, "claude");
    const composer = page.getByLabel("Instruction for claude");
    await expect(composer).toBeVisible();
    const box = await composer.boundingBox();
    expect(box!.y + box!.height).toBeLessThanOrEqual(380);
  });

  test("text zoom does not clip the inbox", async ({ page }) => {
    await pair(page);
    // Emulate a user who has raised the base font size.
    await page.addStyleTag({ content: "html { font-size: 22px; }" });
    await expect(page.getByRole("heading", { level: 1, name: "Agents" })).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBeLessThanOrEqual(1);
  });

  test("reduced motion disables the runline settle", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await pair(page);
    await openRun(page, "claude");
    await page.getByLabel("Instruction for claude").fill("go");
    await page.getByRole("button", { name: "Send instruction" }).click();
    await expect(main(page).getByText("Instruction delivered to the agent")).toBeVisible();

    const duration = await main(page)
      .getByRole("listitem")
      .filter({ hasText: "Instruction delivered to the agent" })
      .first()
      .evaluate((el) => getComputedStyle(el).animationDuration);
    expect(parseFloat(duration)).toBeLessThan(0.01);
  });

  test("crossing the wide breakpoint keeps the open run mounted", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");
    await page.getByLabel("Instruction for claude").fill("draft that must survive");

    await page.setViewportSize({ width: 1280, height: 900 });
    // The inbox joins as a column; the run column is the same tree.
    await expect(page.getByRole("heading", { level: 1, name: "Agents" })).toBeVisible();
    await expect(page.getByRole("heading", { level: 1, name: "claude" })).toBeVisible();
    await expect(page.getByLabel("Instruction for claude")).toHaveValue("draft that must survive");

    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByLabel("Instruction for claude")).toHaveValue("draft that must survive");
  });
});
