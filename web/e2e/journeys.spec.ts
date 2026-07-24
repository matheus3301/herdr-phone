import { test, expect } from "@playwright/test";
import {
  failNext,
  failNextRunRead,
  goTo,
  inbox,
  instructions,
  main,
  openRun,
  openWorkspace,
  pair,
  replacePane,
  setRunContract,
} from "./helpers";

test.describe("Agents inbox", () => {
  test("pair, land in the inbox, and open an existing run", async ({ page }) => {
    await pair(page);

    // Attention leads, and every section is named in the product's vocabulary.
    const headings = inbox(page).getByRole("heading", { level: 2 });
    await expect(headings.first()).toContainText(/needs you/i);
    await expect(inbox(page).getByRole("heading", { level: 2, name: /updated/i })).toBeVisible();
    await expect(inbox(page).getByRole("heading", { level: 2, name: /status unknown/i })).toBeVisible();
    // "done" is never dressed up as an outcome Herdr did not report.
    await expect(inbox(page).getByRole("heading", { level: 2, name: /ready|successful/i })).toHaveCount(0);

    await openRun(page, "claude");
    await expect(main(page).getByText("A decision is required before this run can continue.")).toBeVisible();
  });

  test("opening a run does not move focus on the Mac", async ({ page }) => {
    await pair(page);

    // Passive observation: `page.on("request")` sees every mutation without
    // interposing a handler that WebKit and a registered service worker can
    // disagree about.
    const focusCalls: string[] = [];
    page.on("request", (request) => {
      if (!request.url().includes("/api/v1/mutations")) return;
      const body = JSON.parse(request.postData() ?? "{}") as { operation?: string };
      if (body.operation) focusCalls.push(body.operation);
    });

    await openRun(page, "claude");
    await expect(main(page).getByRole("heading", { name: /observed activity/i })).toBeVisible();
    expect(focusCalls.filter((op) => op.endsWith(".focus"))).toEqual([]);
  });

  test("the terminal is not a primary destination", async ({ page }) => {
    await pair(page);
    const nav = page.getByRole("navigation", { name: "Primary" });
    await expect(nav.getByRole("link", { name: "Agents" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Start run" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Workspaces" })).toBeVisible();
    await expect(nav.getByRole("link", { name: /terminal|console/i })).toHaveCount(0);
  });
});

test.describe("Run detail", () => {
  test("send an instruction and watch it reach Delivered", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");

    const composer = page.getByLabel("Instruction for claude");
    // The exact target is stated before anything is sent.
    await expect(main(page).getByText("claude · space-api / auth-refactor")).toBeVisible();
    await composer.fill("continue with the reconnect fix");
    await page.getByRole("button", { name: "Send instruction" }).click();

    await expect(instructions(page).getByText("continue with the reconnect fix")).toBeVisible();
    await expect(instructions(page).getByText("Delivered")).toBeVisible();
    await expect(composer).toHaveValue("");
    // The delivered instruction is recorded on the runline.
    await expect(main(page).getByText("Instruction delivered to the agent")).toBeVisible();
  });

  test("a draft survives a connection loss and is never silently dropped", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");

    const composer = page.getByLabel("Instruction for claude");
    await composer.fill("restart the focused tests");

    await page.request.post("/api/v1/__outage", { data: { on: true } });
    await expect(page.getByText(/can't reach your mac|reconnecting to the relay/i).first()).toBeVisible({ timeout: 25_000 });
    await expect(composer).toHaveValue("restart the focused tests");
    await expect(page.getByText(/your draft is kept here until the link is back/i)).toBeVisible();

    await page.request.post("/api/v1/__outage", { data: { on: false } });
    await expect(page.getByText(/your draft is kept here/i)).toBeHidden({ timeout: 30_000 });
    await expect(composer).toHaveValue("restart the focused tests");
  });

  test("a rejected send keeps the instruction text in the box", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");
    await failNext(page, "agent.prompt", { status: 400, code: "invalid_params", message: "prompt refused", retryable: false });

    const composer = page.getByLabel("Instruction for claude");
    await composer.fill("do the thing");
    await page.getByRole("button", { name: "Send instruction" }).click();

    await expect(instructions(page).getByText("Not sent")).toBeVisible();
    await expect(instructions(page).getByText("prompt refused")).toBeVisible();
    await expect(composer).toHaveValue("do the thing");
  });

  test("delivery unknown is surfaced and never retried automatically", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");

    const prompts: unknown[] = [];
    page.on("request", (request) => {
      if (!request.url().includes("/api/v1/mutations")) return;
      const body = JSON.parse(request.postData() ?? "{}") as { operation?: string };
      if (body.operation === "agent.prompt") prompts.push(body);
    });

    // A retryable relay failure means Herdr may already hold the prompt.
    await failNext(page, "agent.prompt", {
      status: 504,
      code: "deadline_exceeded",
      message: "operation outcome uncertain",
      retryable: true,
    });

    await page.getByLabel("Instruction for claude").fill("deploy to staging");
    await page.getByRole("button", { name: "Send instruction" }).click();

    await expect(instructions(page).getByText("Delivery unknown")).toBeVisible();
    await expect(instructions(page).getByText(/may already have received this/i)).toBeVisible();
    await expect(page.getByRole("button", { name: "Send again" })).toBeVisible();

    // Give any (nonexistent) automatic retry time to fire.
    await page.waitForTimeout(2500);
    expect(prompts).toHaveLength(1);

    // Sending again is a deliberate act, and it is a second request.
    await page.getByRole("button", { name: "Send again" }).click();
    await expect.poll(() => prompts.length).toBe(2);
  });

  test("every pane mutation carries the canonical pane id and a live generation", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");

    const sent: Array<{ operation: string; params: Record<string, unknown>; expected_generation?: number }> = [];
    page.on("request", (request) => {
      if (!request.url().includes("/api/v1/mutations")) return;
      sent.push(JSON.parse(request.postData() ?? "{}"));
    });

    await page.getByLabel("Instruction for claude").fill("status?");
    await page.getByRole("button", { name: "Send instruction" }).click();
    await expect(instructions(page).getByText("Delivered")).toBeVisible();

    await page.getByRole("button", { name: /actions for claude/i }).click();
    await page.getByRole("menuitem", { name: /focus this agent on the mac/i }).click();

    await expect.poll(() => sent.length).toBeGreaterThanOrEqual(2);
    for (const request of sent) {
      expect(request.params.pane_id).toBe("w1:p1");
      expect(request.params).not.toHaveProperty("target");
      expect(request.expected_generation).toBe(3);
    }
  });

  test("a stale generation is refused by the relay and reported, not swallowed", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");

    // Recycle the pane behind the UI: same id, new generation, no agent.
    await replacePane(page, "w1:p1");

    // The run is frozen and points at what actually happened.
    await expect(page.getByRole("heading", { level: 1, name: /pane was replaced|agent has ended/i })).toBeVisible({
      timeout: 20_000,
    });
    await expect(main(page).getByText(/generation you were on/i)).toBeVisible();
    await expect(main(page).getByRole("link", { name: /open console/i })).toBeVisible();
  });

  test("the relay rejects a prompt whose generation has moved on", async ({ page }) => {
    await pair(page);

    // Send a prompt with a deliberately stale generation, bypassing the UI, to
    // prove the mock enforces the production guard.
    const stale = await page.request.post("/api/v1/mutations", {
      data: {
        request_id: "e2e-stale",
        operation: "agent.prompt",
        deadline_unix_ms: Date.now() + 10_000,
        expected_generation: 1,
        params: { pane_id: "w1:p1", text: "hi" },
      },
    });
    expect(stale.status()).toBe(409);
    expect(await stale.json()).toMatchObject({ error: { code: "generation_stale" } });

    // And with no generation at all.
    const missing = await page.request.post("/api/v1/mutations", {
      data: {
        request_id: "e2e-missing",
        operation: "agent.prompt",
        deadline_unix_ms: Date.now() + 10_000,
        params: { pane_id: "w1:p1", text: "hi" },
      },
    });
    expect(missing.status()).toBe(400);
    expect(await missing.json()).toMatchObject({ error: { code: "generation_stale" } });

    // And a divergent `target`, which the dispatcher would prefer over pane_id.
    const divergent = await page.request.post("/api/v1/mutations", {
      data: {
        request_id: "e2e-divergent",
        operation: "agent.prompt",
        deadline_unix_ms: Date.now() + 10_000,
        expected_generation: 3,
        params: { pane_id: "w1:p1", target: "opencode", text: "hi" },
      },
    });
    expect(divergent.status()).toBe(400);
    expect(await divergent.json()).toMatchObject({ error: { code: "bad_request" } });
  });
});

test.describe("Structured run contract", () => {
  test("production run mode: the inbox and the run detail come from the run routes", async ({ page }) => {
    const requests: string[] = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname.startsWith("/api/v1/runs") || url.pathname.includes("/read")) requests.push(url.pathname + url.search);
    });

    await pair(page);
    await expect.poll(() => requests.some((r) => r.startsWith("/api/v1/runs"))).toBe(true);

    // The row addresses the run by the relay's authoritative id.
    const row = inbox(page).getByRole("link", { name: /\bclaude\b/ }).first();
    await expect(row).toHaveAttribute("href", "/runs/w1%3Ap1%403");
    await openRun(page, "claude");

    // The detail read is guarded by the mandatory generation, and the legacy
    // pane read is not used at all.
    await expect
      .poll(() => requests.find((r) => r.startsWith("/api/v1/runs/w1%3Ap1")))
      .toContain("expected_generation=3");
    expect(requests.some((r) => r.includes("/panes/"))).toBe(false);

    // Terminal output is labelled as terminal output, and nothing claims to be
    // an agent message, a tool call, an approval, a diff, or a test result.
    await expect(main(page).getByText(/recent terminal output/i)).toBeVisible();
    await expect(main(page).getByText(/not the agent's own messages/i)).toBeVisible();
    await expect(main(page).getByText(/claude --resume/)).toBeVisible();
    await expect(main(page).getByRole("heading", { name: /assistant|conversation|tool calls|diff|test results/i })).toHaveCount(0);
  });

  test("run content is never cached", async ({ page }) => {
    await pair(page);
    const list = await page.request.get("/api/v1/runs");
    expect(list.headers()["cache-control"]).toBe("no-store");
    const detail = await page.request.get("/api/v1/runs/w1%3Ap1?expected_generation=3");
    expect(detail.headers()["cache-control"]).toBe("no-store");
  });

  test("the relay refuses a run read whose generation is absent, zero, or stale", async ({ page }) => {
    await pair(page);

    const missing = await page.request.get("/api/v1/runs/w1%3Ap1");
    expect(missing.status()).toBe(400);
    expect(await missing.json()).toMatchObject({ error: { code: "generation_stale" } });

    const zero = await page.request.get("/api/v1/runs/w1%3Ap1?expected_generation=0");
    expect(zero.status()).toBe(400);

    const unparseable = await page.request.get("/api/v1/runs/w1%3Ap1?expected_generation=later");
    expect(unparseable.status()).toBe(400);

    const stale = await page.request.get("/api/v1/runs/w1%3Ap1?expected_generation=1");
    expect(stale.status()).toBe(409);
    expect(await stale.json()).toMatchObject({ error: { code: "generation_stale" } });

    // A live pane with no agent is not a run.
    const shell = await page.request.get("/api/v1/runs/w1%3Ap2?expected_generation=1");
    expect(shell.status()).toBe(404);
    expect(await shell.json()).toMatchObject({ error: { code: "run_unavailable" } });

    // A pane that is gone is a generation failure, not a missing route.
    const gone = await page.request.get("/api/v1/runs/nope?expected_generation=1");
    expect(gone.status()).toBe(409);
  });

  test("a run read failure is reported with a static message", async ({ page }) => {
    await pair(page);
    await failNextRunRead(page, { status: 502, code: "run_read_failed", message: "run output unavailable" });
    await openRun(page, "claude");

    await expect(main(page).getByText(/herdr could not read this pane/i)).toBeVisible();
    await expect(main(page).getByRole("link", { name: /open console/i }).first()).toBeVisible();
  });

  test("a run invalidates when its pane generation moves on, and is never rebound", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");
    await replacePane(page, "w1:p1");

    await expect(page.getByRole("heading", { level: 1, name: /pane was replaced|agent has ended/i })).toBeVisible({
      timeout: 20_000,
    });
    await expect(main(page).getByText(/generation you were on/i)).toBeVisible();
    // The frozen run still reports the incarnation it was opened at.
    await expect(main(page).getByText("w1:p1")).toBeVisible();
  });

  test("the inbox says when the relay truncated the list", async ({ page }) => {
    await pair(page);
    await setRunContract(page, { max_runs: 2 });

    await expect(inbox(page).getByText(/returned only the first 2 runs/i)).toBeVisible({ timeout: 20_000 });
    await expect(inbox(page).getByText(/some runs are not listed here/i)).toBeVisible();
  });

  test("observed output truncation is reported, not silently dropped", async ({ page }) => {
    await pair(page);
    // Pad the pane past the relay's byte bound so the tail is kept and flagged.
    await setRunContract(page, { output_padding: 80_000 });
    await openRun(page, "claude");

    await expect(main(page).getByText(/older output was dropped/i)).toBeVisible({ timeout: 20_000 });
  });
});

test.describe("Old-relay fallback", () => {
  test("a relay without the run contract still lists and opens runs", async ({ page }) => {
    await page.request.post("/api/v1/__reset");
    await setRunContract(page, { supported: false });

    const runRoutes: string[] = [];
    const paneReads: string[] = [];
    page.on("request", (request) => {
      const path = new URL(request.url()).pathname;
      if (path.startsWith("/api/v1/runs")) runRoutes.push(path);
      if (path.includes("/api/v1/panes/")) paneReads.push(path);
    });

    await pair(page, { reset: false });

    // Internal ids, and no run-route traffic at all: the UI fails closed on the
    // capability document rather than probing a route that may not exist.
    const row = inbox(page).getByRole("link", { name: /\bclaude\b/ }).first();
    await expect(row).toHaveAttribute("href", "/runs/w1%3Ap1~g3");
    await openRun(page, "claude");

    await expect(main(page).getByText(/recent terminal output/i)).toBeVisible();
    await expect(main(page).getByText(/not a transcript/i)).toBeVisible();
    await expect(main(page).getByText(/claude --resume/)).toBeVisible();
    await expect.poll(() => paneReads.length).toBeGreaterThan(0);
    expect(runRoutes).toEqual([]);
  });

  test("a fallback run still sends the canonical pane id and generation", async ({ page }) => {
    await page.request.post("/api/v1/__reset");
    await setRunContract(page, { supported: false });
    await pair(page, { reset: false });
    await openRun(page, "claude");

    const sent: Array<{ params: Record<string, unknown>; expected_generation?: number }> = [];
    page.on("request", (request) => {
      if (!request.url().includes("/api/v1/mutations")) return;
      sent.push(JSON.parse(request.postData() ?? "{}"));
    });

    await page.getByLabel("Instruction for claude").fill("status?");
    await page.getByRole("button", { name: "Send instruction" }).click();
    await expect(instructions(page).getByText("Delivered")).toBeVisible();

    expect(sent[0].params.pane_id).toBe("w1:p1");
    expect(sent[0].params).not.toHaveProperty("target");
    expect(sent[0].expected_generation).toBe(3);
  });
});

test.describe("Console recovery", () => {
  test("a blocked run reaches the console in one tap and can take over", async ({ page, context }) => {
    await pair(page);
    await openRun(page, "claude");
    await main(page).getByRole("link", { name: /open console/i }).first().click();

    await expect(page.getByTestId("terminal-host")).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId("terminal-host")).toContainText("herdr", { timeout: 15_000 });
    await expect(main(page).getByText(/generation 3/)).toBeVisible();

    // A second controller must be offered an explicit, confirmed takeover.
    const second = await context.newPage();
    await pair(second, { reset: false });
    await second.goto("/console/w1%3Ap1?generation=3");
    const takeover = second.getByRole("button", { name: /take over/i });
    await expect(takeover).toBeVisible({ timeout: 20_000 });
    await takeover.click();
    await expect(takeover).toBeHidden();
    await second.close();
  });

  test("an unknown-status run says so and offers the console", async ({ page }) => {
    await pair(page);
    await openRun(page, "cursor");
    await expect(main(page).getByText(/herdr cannot read this agent's state/i)).toBeVisible();
    await expect(main(page).getByRole("link", { name: /open console/i }).first()).toBeVisible();
  });

  test("the console refuses a pane whose generation has moved on", async ({ page }) => {
    await pair(page);
    await replacePane(page, "w1:p1");
    await page.goto("/console/w1%3Ap1?generation=3");
    // The live snapshot wins: the assertion is stale, and the UI says so rather
    // than attaching to a different incarnation.
    await expect(page.getByText(/this pane was replaced since you opened the link/i)).toBeVisible({ timeout: 20_000 });
  });
});

test.describe("Start run", () => {
  test("start a run in an existing workspace", async ({ page }) => {
    await pair(page);
    await goTo(page, "Start run");

    await page.getByLabel("What should the agent do?").fill("Tidy the reconnect path");
    await page.getByRole("radio", { name: /an existing workspace/i }).click();
    await page.getByLabel("Workspace").selectOption("w3");
    await page.getByRole("radio", { name: "codex", exact: true }).click();
    await page.getByLabel("Name it").fill("codex-2");
    await page.getByRole("button", { name: "Start run" }).click();

    await expect(page.getByRole("heading", { level: 1, name: "Run started" })).toBeVisible({ timeout: 30_000 });
    await page.getByRole("button", { name: /open the run/i }).click();
    await expect(page.getByRole("heading", { level: 1, name: "codex-2" })).toBeVisible();
  });

  test("start a run in a new git worktree", async ({ page }) => {
    await pair(page);
    await goTo(page, "Start run");

    await page.getByLabel("What should the agent do?").fill("Prototype the new nav");
    await page.getByRole("radio", { name: /a new git worktree/i }).click();
    await page.getByLabel("New branch").fill("feature/nav");
    await page.getByRole("radio", { name: "gemini", exact: true }).click();
    await page.getByLabel("Name it").fill("gemini-2");
    await page.getByRole("button", { name: "Start run" }).click();

    await expect(page.getByRole("heading", { level: 1, name: "Run started" })).toBeVisible({ timeout: 30_000 });
    await expect(main(page).getByText("Created worktree feature/nav")).toBeVisible();
    await goTo(page, "Workspaces");
    await expect(main(page).getByText("feature/nav").first()).toBeVisible();
  });

  test("a failed agent start keeps the workspace that was created", async ({ page }) => {
    await pair(page);
    await goTo(page, "Start run");

    await page.getByLabel("What should the agent do?").fill("Investigate the flake");
    await page.getByRole("radio", { name: /a new workspace/i }).click();
    await page.getByLabel("Workspace name").fill("flake-hunt");
    await page.getByRole("radio", { name: "claude", exact: true }).click();
    await page.getByLabel("Name it").fill("claude-2");

    await failNext(page, "agent.start", { status: 409, code: "conflict", message: "agent name in use" });
    await page.getByRole("button", { name: "Start run" }).click();

    await expect(page.getByRole("heading", { level: 1, name: "Run partly started" })).toBeVisible({ timeout: 30_000 });
    await expect(main(page).getByText("Created workspace flake-hunt")).toBeVisible();
    await expect(main(page).getByText("agent name in use")).toBeVisible();
    await expect(main(page).getByText(/nothing that succeeded has been undone/i)).toBeVisible();

    // The workspace really is still there.
    await goTo(page, "Workspaces");
    await expect(main(page).getByText("flake-hunt")).toBeVisible();

    // Returning to the launch resumes it: the route is real, not a transient
    // sheet whose state vanishes on dismissal.
    await goTo(page, "Start run");
    await expect(main(page).getByText("Created workspace flake-hunt")).toBeVisible();
    await page.getByRole("button", { name: /retry the failed step/i }).click();
    await expect(page.getByRole("heading", { level: 1, name: "Run started" })).toBeVisible({ timeout: 30_000 });
  });

  test("a partly-composed launch survives leaving the route", async ({ page }) => {
    await pair(page);
    await goTo(page, "Start run");
    await page.getByLabel("What should the agent do?").fill("Half-written objective");
    await goTo(page, "Workspaces");
    await goTo(page, "Start run");
    await expect(page.getByLabel("What should the agent do?")).toHaveValue("Half-written objective");
  });
});

test.describe("Workspaces", () => {
  test("manage a workspace through the advanced controls", async ({ page }) => {
    await pair(page);
    await goTo(page, "Workspaces");

    await expect(page.getByRole("heading", { level: 1, name: "Workspaces" })).toBeVisible();
    await openWorkspace(page, "space-api");

    // Topology lives here, with generations on show.
    await expect(main(page).getByText(/generation 3/).first()).toBeVisible();

    // Rename a tab.
    await page.getByRole("button", { name: /rename tests/i }).click();
    await page.getByLabel("Label").fill("integration");
    await page.getByRole("button", { name: /^save$/i }).click();
    await expect(page.getByRole("heading", { level: 3, name: /integration/ })).toBeVisible({ timeout: 15_000 });

    // Reorder tabs.
    await page.getByRole("button", { name: /move integration left/i }).click();
    await expect(page.getByRole("button", { name: /move integration left/i })).toBeDisabled({ timeout: 15_000 });

    // Split a pane through the pane sheet.
    await page.getByRole("button", { name: /actions for server/i }).click();
    const paneSheet = page.getByRole("dialog");
    await expect(paneSheet.getByText(/generation 1/)).toBeVisible();
    await paneSheet.getByRole("button", { name: /split right/i }).click();
    await page.keyboard.press("Escape");
    await expect(main(page).getByText(/w1:p9|w1:p10/).first()).toBeVisible({ timeout: 15_000 });

    // The danger zone is behind an explicit disclosure.
    await expect(page.getByRole("button", { name: /close workspace/i })).toHaveCount(0);
    await page.getByRole("button", { name: /danger zone/i }).click();
    await expect(page.getByRole("button", { name: /close workspace/i })).toBeVisible();
  });

  test("closing a pane needs a confirmation dialog", async ({ page }) => {
    await pair(page);
    await goTo(page, "Workspaces");
    await openWorkspace(page, "infra");
    await page.getByRole("button", { name: /actions for zsh/i }).click();
    await page.getByRole("button", { name: /close pane/i }).click();

    const alert = page.getByRole("alertdialog");
    await expect(alert).toBeVisible();
    await expect(alert).toContainText(/terminated/i);
    await alert.getByRole("button", { name: /^confirm$/i }).click();
    await expect(alert).toBeHidden({ timeout: 15_000 });
  });

  test("start an agent in an empty shell pane", async ({ page }) => {
    await pair(page);
    await goTo(page, "Workspaces");
    await openWorkspace(page, "infra");
    await page.getByRole("button", { name: /actions for zsh/i }).click();
    await page.getByRole("button", { name: /start an agent here/i }).click();
    await page.getByRole("radio", { name: "opencode", exact: true }).click();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("opencode-2");
    await page.getByRole("button", { name: /^start agent$/i }).click();

    await goTo(page, "Agents");
    await expect(inbox(page).getByText("opencode-2").first()).toBeVisible({ timeout: 15_000 });
  });
});

test.describe("Session and connection", () => {
  test("a cold reload recovers a mutable session without re-pairing", async ({ page }) => {
    await pair(page);

    let sessionBody = "";
    page.on("response", async (r) => {
      if (r.request().method() === "GET" && r.url().includes("/api/v1/session")) {
        try {
          sessionBody = await r.text();
        } catch {
          /* body may be unavailable if superseded */
        }
      }
    });

    await page.reload();
    await expect(page.getByRole("heading", { level: 1, name: "Agents" })).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText(/can't reach your mac/i)).toHaveCount(0);

    // A real mutation proves the CSRF token came back.
    await openRun(page, "claude");
    await page.getByLabel("Instruction for claude").fill("still here?");
    await page.getByRole("button", { name: "Send instruction" }).click();
    await expect(instructions(page).getByText("Delivered")).toBeVisible({ timeout: 15_000 });

    await expect.poll(() => sessionBody).toContain("csrf_token");
    expect(sessionBody).not.toContain("hp_mock_session");
  });

  test("a genuine outage names the failure and clears on recovery", async ({ page }) => {
    await pair(page);
    await page.request.post("/api/v1/__outage", { data: { on: true } });
    await expect(page.getByText(/reconnecting to the relay|can't reach your mac/i).first()).toBeVisible({ timeout: 25_000 });
    await page.request.post("/api/v1/__outage", { data: { on: false } });
    await expect(page.getByText(/can't reach your mac/i)).toHaveCount(0, { timeout: 30_000 });
  });

  test("an idle live session raises no false alarm", async ({ page }) => {
    await pair(page);
    await page.waitForTimeout(14_000);
    await expect(page.getByText(/reconnecting to the relay|can't reach your mac/i)).toHaveCount(0);
    await expect(page.getByRole("heading", { level: 1, name: "Agents" })).toBeVisible();
  });
});
