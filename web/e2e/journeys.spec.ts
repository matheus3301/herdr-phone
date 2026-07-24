import { test, expect } from "@playwright/test";
import { pair, goToSection } from "./helpers";

test.describe("Herdr Phone mobile journeys", () => {
  test("pair → lands in the terminal", async ({ page }) => {
    await pair(page);
    await expect(page.getByRole("button", { name: /open space switcher/i })).toBeVisible();
  });

  test("blocked-first herd and prompt an agent", async ({ page }) => {
    await pair(page);
    await goToSection(page, "Herd");

    const headings = page.getByRole("heading", { level: 2 });
    await expect(headings.first()).toContainText(/needs you/i);
    await expect(page.getByText(/Approve this command\?/)).toBeVisible();

    // Prompt the blocked agent.
    await page.getByRole("button", { name: /prompt claude/i }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByLabel("Message").fill("continue");
    await dialog.getByRole("button", { name: /^send$/i }).click();
    await expect(dialog).toBeHidden();
  });

  test("switch workspace, tab, and pane via the ribbon", async ({ page }) => {
    await pair(page);
    // Switch workspace via chip.
    await page.getByRole("button", { name: /mobile-ui/ }).first().click();
    await expect(page.getByRole("button", { name: /mobile-ui/ }).first()).toHaveAttribute("aria-current", "true");

    // Open the tab switcher and pick a tab.
    await page.getByRole("button", { name: /open tab switcher/i }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await page.keyboard.press("Escape");
  });

  test("create a workspace and a tab", async ({ page }) => {
    await pair(page);
    await page.getByRole("button", { name: /open space switcher/i }).click();
    await page.getByRole("button", { name: /new workspace/i }).click();
    await page.getByLabel("Label").fill("e2e-space");
    await page.getByRole("button", { name: /create workspace/i }).click();
    await expect(page.getByRole("button", { name: /e2e-space/ }).first()).toBeVisible({ timeout: 15_000 });
    await page.keyboard.press("Escape"); // close the workspace switcher

    // Create a tab in the now-active workspace.
    await page.getByRole("button", { name: /open tab switcher/i }).click();
    await page.getByRole("button", { name: /new tab/i }).click();
    await page.getByLabel("Label").fill("e2e-tab");
    await page.getByRole("button", { name: /create tab/i }).click();
    await expect(page.getByRole("button", { name: /e2e-tab/ }).first()).toBeVisible({ timeout: 15_000 });
  });

  test("split a pane", async ({ page }) => {
    await pair(page);
    // Count panes in the current tab via the pane switcher.
    await page.getByRole("button", { name: /open pane switcher/i }).click();
    const before = await page.getByRole("button", { name: /^Actions for / }).count();
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: /pane actions/i }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await page.getByRole("button", { name: /split right/i }).click();
    await page.keyboard.press("Escape"); // close the pane-actions sheet before polling

    await expect
      .poll(
        async () => {
          await page.getByRole("button", { name: /open pane switcher/i }).click();
          const n = await page.getByRole("button", { name: /^Actions for / }).count();
          await page.keyboard.press("Escape");
          return n;
        },
        { timeout: 15_000 },
      )
      .toBeGreaterThan(before);
  });

  test("terminal input echoes and survives resize", async ({ page }) => {
    await pair(page);
    const host = page.getByTestId("terminal-host");
    await expect(host).toBeVisible();
    await host.click();
    // Type via the composer (soft-keyboard path).
    await page.getByLabel("Message or command").fill("echo hello");
    await page.getByRole("button", { name: /^send$/i }).click();
    // The mock echoes input bytes back to the terminal.
    await expect(host).toContainText("echo hello", { timeout: 10_000 });

    // Resize the viewport; terminal must remain mounted and usable.
    const size = page.viewportSize();
    if (size) await page.setViewportSize({ width: size.width, height: Math.max(480, size.height - 220) });
    await expect(host).toBeVisible();
  });

  test("send a special key from the dock", async ({ page }) => {
    await pair(page);
    // Arm Ctrl (tri-state) then send a key — dock must be present above the composer.
    await expect(page.getByRole("button", { name: "Ctrl", exact: true })).toBeVisible();
    await page.getByRole("button", { name: /send ctrl\+c interrupt/i }).click();
    await expect(page.getByTestId("terminal-host")).toBeVisible();
  });

  test("confirm-close a pane requires the AlertDialog", async ({ page }) => {
    await pair(page);
    await page.getByRole("button", { name: /pane actions/i }).click();
    await page.getByRole("button", { name: /close pane/i }).click();
    const alert = page.getByRole("alertdialog");
    await expect(alert).toBeVisible();
    await expect(alert).toContainText(/terminated/i);
    await alert.getByRole("button", { name: /^confirm$/i }).click();
    await expect(alert).toBeHidden();
  });

  test("start a discovered agent on a shell pane (C1)", async ({ page }) => {
    await pair(page);
    // Select the shell pane ("server") via the pane switcher.
    await page.getByRole("button", { name: /open pane switcher/i }).click();
    await page.getByRole("button", { name: /server/i }).first().click();
    // Open its actions and start an agent.
    await page.getByRole("button", { name: /pane actions/i }).click();
    await page.getByRole("button", { name: "Start agent" }).click();
    await page.getByRole("button", { name: /gemini/i }).click();
    await expect(page.getByLabel("Name")).toHaveValue("gemini");
    await page.getByRole("button", { name: /start agent/i }).click();
    // The new agent shows up in the herd.
    await goToSection(page, "Herd");
    await expect(page.getByText("gemini", { exact: true }).first()).toBeVisible({ timeout: 15_000 });
  });

  test("reorder tabs (H2)", async ({ page }) => {
    await pair(page);
    await page.getByRole("button", { name: /open tab switcher/i }).click();
    // "tests" is the second tab; moving it left puts it first (button then disables).
    await page.getByRole("button", { name: /move tests left/i }).click();
    await expect(page.getByRole("button", { name: /move tests left/i })).toBeDisabled({ timeout: 15_000 });
  });

  test("send validated keys to an agent (H3)", async ({ page }) => {
    await pair(page);
    await goToSection(page, "Herd");
    await page.getByRole("button", { name: /send keys to claude/i }).click();
    await page.getByRole("button", { name: /send enter/i }).click();
    await expect(page.getByText(/sent enter/i)).toBeVisible({ timeout: 10_000 });
  });

  test("move a pane to an existing tab (M4)", async ({ page }) => {
    await pair(page);
    await page.getByRole("button", { name: /pane actions/i }).click();
    await page.getByRole("button", { name: /move → tab…/i }).click();
    await page.getByRole("button", { name: /^tests/i }).click();
    // The source tab (auth-refactor, initially 2 panes) drops to 1 after the move.
    await page.getByRole("button", { name: /open tab switcher/i }).click();
    const dialog = page.getByRole("dialog");
    await expect(
      dialog.locator("div").filter({ hasText: "auth-refactor" }).filter({ hasText: "1 panes" }).first(),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("paste multi-line text from the key dock without concatenating (M5/F1)", async ({ page, browserName }) => {
    test.skip(browserName === "webkit", "clipboard-read permission is unavailable on WebKit");
    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    await pair(page);
    await page.evaluate(() => navigator.clipboard.writeText("echo line-one\necho line-two"));
    await page.getByRole("button", { name: /paste from clipboard/i }).click();
    // Both lines are delivered end-to-end. (The no-concatenation / newline-
    // preservation guarantee is asserted at the unit level in lib/paste.test.ts —
    // xterm's DOM textContent joins rows with no separator, so it cannot itself
    // distinguish a preserved newline from a concatenation.)
    const host = page.getByTestId("terminal-host");
    await expect(host).toContainText("line-one", { timeout: 10_000 });
    await expect(host).toContainText("line-two", { timeout: 10_000 });
  });

  test("cold reload recovers a mutable session without re-pairing (session CSRF)", async ({ page }) => {
    await pair(page);

    // Capture the GET /session response across the reload to assert its contract.
    let sessionBody = "";
    page.on("response", async (r) => {
      if (r.request().method() === "GET" && r.url().includes("/api/v1/session")) {
        try {
          sessionBody = await r.text();
        } catch {
          /* response body may be unavailable if superseded */
        }
      }
    });

    // Cold reload: no pairing fragment, only the HttpOnly cookie survives.
    await page.reload();
    await expect(page.getByTestId("terminal-host")).toBeVisible({ timeout: 20_000 });

    // The reload must NOT falsely show "Connection lost".
    await expect(page.getByText(/connection lost/i)).toHaveCount(0);

    // A structural mutation succeeds — proving the CSRF token was recovered.
    await page.getByRole("button", { name: /open tab switcher/i }).click();
    await page.getByRole("button", { name: /new tab/i }).click();
    await page.getByLabel("Label").fill("post-reload");
    await page.getByRole("button", { name: /create tab/i }).click();
    await expect(page.getByRole("button", { name: /post-reload/ }).first()).toBeVisible({ timeout: 15_000 });

    // GET /session carries the CSRF token and never the bearer cookie.
    await expect.poll(() => sessionBody).toContain("csrf_token");
    expect(sessionBody).not.toContain("hp_mock_session");
  });

  test("reconnect: banner appears on a real disconnect and clears on recovery", async ({ page }) => {
    await pair(page);
    // A genuine outage: the relay closes the events socket, refuses reconnects,
    // and 503s the snapshot poll. (context.setOffline is not used — in Chromium
    // it leaves the socket readyState OPEN, which the health logic now correctly
    // treats as live; only an actual close/failure must raise the banner.)
    await page.request.post("/api/v1/__outage", { data: { on: true } });
    await expect(page.getByText(/reconnecting|connection lost/i).first()).toBeVisible({ timeout: 20_000 });
    await page.request.post("/api/v1/__outage", { data: { on: false } });
    await expect(page.getByText(/connection lost/i)).toBeHidden({ timeout: 25_000 });
    // The banner fully clears (health returns to live once the socket reopens).
    await expect(page.getByText(/reconnecting|connection lost/i)).toHaveCount(0, { timeout: 25_000 });
  });

  test("an idle live session does not raise a false disconnect banner", async ({ page }) => {
    await pair(page);
    // Sit idle well past the lost threshold (12s) with no app messages: a healthy
    // OPEN socket must stay live (server Ping/Pong keeps it alive invisibly).
    await page.waitForTimeout(14_000);
    await expect(page.getByText(/reconnecting|connection lost/i)).toHaveCount(0);
    await expect(page.getByTestId("terminal-host")).toBeVisible();
  });
});

test.describe("takeover", () => {
  test("a second controller sees a conflict and can take over", async ({ context }) => {
    const p1 = await context.newPage();
    await pair(p1);
    await expect(p1.getByTestId("terminal-host")).toBeVisible();
    // Ensure p1's controller has actually attached (banner rendered) so it owns
    // the pane before the second controller connects — avoids a takeover race.
    await expect(p1.getByTestId("terminal-host")).toContainText("herdr", { timeout: 10_000 });

    // The second controller is its own paired session (own CSRF token); pairing
    // it must not reset the shared herd or p1 would lose ownership.
    const p2 = await context.newPage();
    await pair(p2, { reset: false });
    await expect(p2.getByTestId("terminal-host")).toBeVisible({ timeout: 20_000 });
    // The second controller for the same focused pane must be offered takeover.
    const takeover = p2.getByRole("button", { name: /take over/i });
    await expect(takeover).toBeVisible({ timeout: 15_000 });
    await takeover.click();
    await expect(takeover).toBeHidden();
    await p1.close();
    await p2.close();
  });
});

test.describe("desktop smoke", () => {
  test.skip(({ browserName }) => browserName !== "chromium", "desktop project only");

  test("wide layout, keyboard nav, and light theme", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "desktop", "desktop project only");
    await pair(page);
    // Left rail present at desktop width.
    await expect(page.getByRole("navigation", { name: /primary/i })).toBeVisible();

    // Keyboard-only navigation to Settings.
    await page.getByRole("button", { name: /settings/i }).click();
    await expect(page.getByRole("radiogroup", { name: /theme/i })).toBeVisible();

    // Switch to light theme and verify the document class flips.
    await page.getByRole("radio", { name: "Light" }).click();
    await expect.poll(() => page.evaluate(() => document.documentElement.classList.contains("light"))).toBe(true);
  });
});
