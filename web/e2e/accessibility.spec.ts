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
      await expect(page.getByRole("link", { name: /skip to content/i })).toBeAttached();
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

  // QA A11Y-1. The link used to point at `#main` unconditionally, but below the
  // wide breakpoint `#main` is `display:none` on `/` — so on every phone and
  // tablet width the link skipped *over* all the visible content and landed in
  // the bottom nav. Activating it is the only way to catch that, and the target
  // has to be focusable for `activeElement` to move at all.
  test("skip navigation reaches visible content on both layouts", async ({ page }) => {
    for (const width of [320, 390, 430, 1440]) {
      await page.setViewportSize({ width, height: 800 });
      await pair(page);

      // Inbox layout: `/`.
      const link = page.getByRole("link", { name: /skip to content/i });
      const href = await link.getAttribute("href");
      const target = page.locator(href!);
      await expect(target, `skip target visible at ${width} on /`).toBeVisible();
      await link.evaluate((el) => (el as HTMLAnchorElement).click());
      const landedId = await page.evaluate(() => document.activeElement?.id ?? "");
      expect(landedId, `focus landed inside content at ${width} on /`).toBe(href!.slice(1));

      // Detail layout: an open run.
      await openRun(page, "claude");
      const detailHref = await page.getByRole("link", { name: /skip to content/i }).getAttribute("href");
      const detailTarget = page.locator(detailHref!);
      await expect(detailTarget, `skip target visible at ${width} on a run`).toBeVisible();
      await page.getByRole("link", { name: /skip to content/i }).evaluate((el) => (el as HTMLAnchorElement).click());
      const detailLanded = await page.evaluate(() => document.activeElement?.id ?? "");
      expect(detailLanded, `focus landed inside content at ${width} on a run`).toBe(detailHref!.slice(1));
    }
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
    const log = page.getByRole("log", { name: /observed activity/i });
    await expect(log).toBeVisible();
    await expect(log).toHaveAttribute("aria-live", "polite");
  });

  /**
   * Review MEDIUM 1.
   *
   * The polite region used to be the whole scroll container, terminal tail
   * included. `useRunOutput` re-reads on every refresh and replaces that block's
   * text — additions, under `aria-relevant="additions"` — so a screen reader
   * re-announced up to 40 lines of terminal bytes each time. The regions that
   * genuinely gain entries announce; the tail must be silent.
   */
  test("refreshed terminal output is never re-announced", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");
    await expect(page.getByText(/recent terminal output/i)).toBeVisible();

    const tail = page.locator("section[aria-labelledby='recent-output-heading']");
    await expect(tail).toHaveAttribute("aria-live", "off");

    // No live ancestor may reintroduce it: walking up from the tail must find no
    // element that announces, so a refresh cannot be read out at all.
    const liveAncestor = await tail.evaluate((el) => {
      for (let node = el.parentElement; node; node = node.parentElement) {
        const live = node.getAttribute("aria-live");
        if (live && live !== "off") return `${node.tagName}.${node.className}`;
        if (node.getAttribute("role") === "log") return `${node.tagName}[role=log]`;
      }
      return null;
    });
    expect(liveAncestor, "terminal output sits inside a live region").toBeNull();

    // The instruction list and the observed feed are the two that do announce.
    // The instruction section only exists once there is an instruction in it.
    await expect(page.getByRole("log", { name: /observed activity/i })).toHaveAttribute("aria-live", "polite");
    await page.getByLabel("Instruction for claude").fill("carry on");
    await page.getByRole("button", { name: "Send instruction" }).click();
    await expect(instructions(page).getByText("Delivered")).toBeVisible();
    await expect(instructions(page)).toHaveAttribute("aria-live", "polite");
  });

  test("interactive targets clear 44px in both dimensions", async ({ page }) => {
    await pair(page);
    const nav = page.getByRole("navigation", { name: "Primary" });
    for (const name of ["Agents", "Start run", "Workspaces"]) {
      const box = await nav.getByRole("link", { name }).boundingBox();
      expect(box, name).not.toBeNull();
      expect(box!.height, `${name} height`).toBeGreaterThanOrEqual(44);
      expect(box!.width, `${name} width`).toBeGreaterThanOrEqual(44);
    }
    const start = await inbox(page).getByRole("link", { name: "Start run" }).boundingBox();
    expect(start!.height).toBeGreaterThanOrEqual(40);

    // QA A11Y-3: the composer's send button is the primary action of a run, and
    // it sits next to a `w-full` textarea — so it is the one control flex will
    // squeeze below the minimum unless it refuses to shrink. Width was never
    // checked before, which is why 38.08 x 44 shipped.
    await openRun(page, "claude");
    const composer = page.getByLabel("Instruction for claude");
    await composer.fill("ready to send");
    const send = page.getByRole("button", { name: "Send instruction" });
    const box = await send.boundingBox();
    expect(box, "send instruction").not.toBeNull();
    expect(box!.width, "send instruction width").toBeGreaterThanOrEqual(44);
    expect(box!.height, "send instruction height").toBeGreaterThanOrEqual(44);
  });

  // QA A11Y-3, generalised: sweep every visible interactive control per route.
  // A pure height check misses a flex-squeezed control entirely.
  test("no visible control is under 44px on either axis", async ({ page }) => {
    await pair(page);
    // At phone width the inbox and the detail column share one grid cell, so the
    // inbox is only reachable from `/` — every hop returns there first.
    const routes: Array<{ name: string; go: () => Promise<void> }> = [
      { name: "/", go: async () => {} },
      { name: "/runs/new", go: async () => goTo(page, "Start run") },
      { name: "/workspaces", go: async () => goTo(page, "Workspaces") },
      { name: "/runs/:id", go: async () => openRun(page, "claude") },
    ];
    for (const route of routes) {
      await goTo(page, "Agents");
      await route.go();
      const undersized = await page.evaluate(() => {
        const bad: Array<{ tag: string; label: string; w: number; h: number }> = [];
        const nodes = document.querySelectorAll("a[href], button, [role=radio], select, textarea, input");
        for (const node of nodes) {
          const el = node as HTMLElement;
          if ((el as HTMLButtonElement).disabled) continue;
          const rect = el.getBoundingClientRect();
          // Off-screen (the skip link) or not rendered: not a target.
          if (rect.width === 0 || rect.height === 0) continue;
          if (rect.right < 0 || rect.bottom < 0) continue;
          if (rect.width >= 44 && rect.height >= 44) continue;
          bad.push({
            tag: el.tagName,
            label: (el.getAttribute("aria-label") || el.textContent || "").trim().slice(0, 40),
            w: Math.round(rect.width),
            h: Math.round(rect.height),
          });
        }
        return bad;
      });
      expect(undersized, `${route.name} has controls under 44px`).toEqual([]);
    }
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

  /**
   * QA A11Y-2 — WCAG 1.4.4 at 200%.
   *
   * The old guard asserted `scrollWidth - innerWidth <= 1` at 137% and passed
   * while controls were being clipped off-screen. That assertion structurally
   * cannot detect this class of bug: the shell sets `overflow: hidden`, so a
   * clipped control produces *zero* overflow — the absence of a scrollbar is
   * precisely what makes it unreachable. Measure the controls instead.
   */
  for (const width of [320, 390, 430]) {
    test(`every control stays reachable at 200% text zoom (${width}px)`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await pair(page);
      // 16px is the browser default, so 32px is 200%.
      await page.addStyleTag({ content: "html { font-size: 32px; }" });
      await expect(page.getByRole("heading", { level: 1, name: "Agents" })).toBeVisible();

      const unreachable = async (label: string) => {
        const out = await page.evaluate(() => {
          const bad: Array<{ label: string; left: number; right: number; view: number }> = [];
          const nodes = document.querySelectorAll("a[href], button, [role=radio], select, textarea, input");
          for (const node of nodes) {
            const el = node as HTMLElement;
            const rect = el.getBoundingClientRect();
            if (rect.width === 0 || rect.height === 0) continue;
            // The skip link parks itself off-screen until focused, by design.
            if (el.classList.contains("skip-link")) continue;
            // A control is reachable when it is inside the viewport OR the page
            // can be scrolled to bring it in. Clipped-with-no-scroll is the bug.
            const scrollable =
              document.documentElement.scrollWidth > window.innerWidth ||
              (el.closest("[data-scroll-x]") as HTMLElement | null) !== null;
            const inside = rect.left >= -1 && rect.right <= window.innerWidth + 1;
            if (inside || scrollable) continue;
            bad.push({
              label: (el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 40),
              left: Math.round(rect.left),
              right: Math.round(rect.right),
              view: window.innerWidth,
            });
          }
          return bad;
        });
        expect(out, `${label} at ${width}px / 200% zoom`).toEqual([]);
      };

      await unreachable("inbox");
      // Settings has no alternate path, so losing it is a loss of functionality.
      await expect(page.getByRole("button", { name: "Settings" })).toBeVisible();
      const settings = await page.getByRole("button", { name: "Settings" }).boundingBox();
      expect(settings!.right ?? 0, `Settings right edge at ${width}px`).toBeLessThanOrEqual(width + 1);

      await goTo(page, "Start run");
      await unreachable("start run");
      await goTo(page, "Agents");
      await openRun(page, "claude");
      await unreachable("run detail");
    });
  }

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
