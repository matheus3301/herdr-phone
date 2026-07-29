import { test, expect, type Page } from "@playwright/test";
import { main, openRun, pair, setInterpretation } from "./helpers";

/**
 * Experimental heuristic interpretation, end to end (SPEC §12.2).
 *
 * These journeys run the real production bundle against the mock relay's exact
 * wire shapes. The three things worth proving on a real device:
 *
 *  1. With the flag off, the run page is unchanged — no chat, no interaction card.
 *  2. With it on, the chat renders, is permanently labelled as a guess, and the
 *     raw terminal output is still reachable.
 *  3. Answering takes two deliberate steps and shows the literal key first; a
 *     prompt the relay marked unanswerable offers no send affordance at all.
 */

/** The interpreted transcript section. */
function chat(page: Page) {
  return main(page).locator("section[aria-labelledby='chat-heading']");
}

/** The interpreted prompt card. */
function interaction(page: Page) {
  return main(page).locator("section[aria-labelledby='interaction-heading']");
}

/** The raw/recent terminal output section. */
function tail(page: Page) {
  return main(page).locator("section[aria-labelledby='recent-output-heading']");
}

/**
 * Pair with interpretation already enabled.
 *
 * Ordering matters: the capability document is read once when the app boots, and
 * the chat is gated on it. `pair()` resets the mock (which turns the flag back off),
 * so the flag has to be set between the reset and the boot — not after pairing.
 */
async function pairInterpreting(page: Page, parsers?: string[]) {
  await page.request.post("/api/v1/__reset");
  await setInterpretation(page, { enabled: true, ...(parsers ? { parsers } : {}) });
  await pair(page, { reset: false });
}

test.describe("Interpretation off — the default", () => {
  test("the run page is unchanged and nothing is interpreted", async ({ page }) => {
    await pair(page);
    await openRun(page, "claude");

    await expect(chat(page)).toHaveCount(0);
    await expect(interaction(page)).toHaveCount(0);
    await expect(main(page).getByText(/experimental reading/i)).toHaveCount(0);

    // Today's presentation: the tail leads and is named for recency. Its label is
    // the collapsible's trigger text, not a heading element.
    await expect(tail(page).getByText("Recent terminal output")).toBeVisible();
    await expect(main(page).getByRole("heading", { level: 2, name: "Observed activity" })).toBeVisible();
  });
});

test.describe("Interpretation on — the chat view", () => {
  test("renders a chat, labels it a guess, and keeps the raw output reachable", async ({ page }) => {
    await pairInterpreting(page);
    await openRun(page, "claude");

    // The chat is present, with the agent's apparent prose and tool activity.
    await expect(chat(page).getByRole("heading", { name: "Conversation" })).toBeVisible();
    await expect(chat(page).getByText(/I'll check the existing file ending/)).toBeVisible();
    await expect(chat(page).getByText("Bash").first()).toBeVisible();

    // The standing label is on screen and is not a dismissible toast.
    await expect(chat(page).getByText(/experimental reading/i).first()).toBeVisible();
    await expect(chat(page).getByText(/not the agent's own messages/i).first()).toBeVisible();

    // The raw bytes are still present and one tap away, renamed to what they now
    // are. Assert the disclosure's own state rather than its inner content, so this
    // does not depend on the collapsible's animation timing.
    await expect(tail(page).getByText("Raw terminal output")).toBeVisible();
    const disclosure = tail(page).getByRole("button", { name: /Raw terminal output/ });
    await expect(disclosure).toHaveAttribute("aria-expanded", "false");
    await disclosure.click();
    await expect(disclosure).toHaveAttribute("aria-expanded", "true");
    await expect(tail(page).getByRole("link", { name: /Open console/i })).toBeVisible();
  });

  test("a detected approval leads the page and states what it is asking", async ({ page }) => {
    await pairInterpreting(page);
    await openRun(page, "claude");

    const card = interaction(page);
    await expect(card.getByRole("heading", { name: /needs permission/i })).toBeVisible();
    await expect(card.getByText("Bash command")).toBeVisible();
    await expect(card.getByText("Do you want to proceed?")).toBeVisible();
    await expect(card.getByText(/echo "hello fixture" >> notes.txt/)).toBeVisible();
  });

  test("answering takes two steps and shows the literal key before sending", async ({ page }) => {
    await pairInterpreting(page);
    await openRun(page, "claude");

    // First tap opens a confirmation; it does not send.
    await interaction(page).getByRole("button", { name: /1\s*Yes$/ }).click();

    const sheet = page.getByRole("dialog");
    await expect(sheet.getByRole("heading", { name: /Send this answer/i })).toBeVisible();
    await expect(sheet.getByText(/Key delivered/i)).toBeVisible();
    await expect(sheet.getByText('"1"')).toBeVisible();
    await expect(sheet.getByText("Yes", { exact: true })).toBeVisible();

    // Cancelling sends nothing and leaves the prompt in place.
    await sheet.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(interaction(page).getByText("Do you want to proceed?")).toBeVisible();

    // Confirming delivers it.
    await interaction(page).getByRole("button", { name: /3\s*No$/ }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Send" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });

  test("an OpenCode prompt is shown but cannot be answered from the phone", async ({ page }) => {
    await pairInterpreting(page);
    await openRun(page, "opencode");

    const card = interaction(page);
    await expect(card.getByRole("heading", { name: /needs permission/i })).toBeVisible();
    await expect(card.getByText("Edit /tmp/sandbox/notes.txt")).toBeVisible();

    // The diff being approved is visible.
    await expect(card.getByText("hello fixture")).toBeVisible();

    // The choices are readable text, never tappable actions.
    await expect(card.getByText(/Allow once/)).toBeVisible();
    await expect(card.getByRole("button", { name: /Allow once/ })).toHaveCount(0);
    await expect(card.getByRole("button", { name: /Reject/ })).toHaveCount(0);

    // And the limitation is explained, with a route to the surface that can answer.
    await expect(card.getByText(/can't be answered from your phone/i)).toBeVisible();
    await expect(card.getByRole("link", { name: /Open console/i })).toBeVisible();
  });

  test("a pane whose agent kind is not configured is not interpreted", async ({ page }) => {
    // Only OpenCode is parsed; the Claude pane must fall back to today's view.
    await pairInterpreting(page, ["opencode"]);
    await openRun(page, "claude");

    await expect(chat(page)).toHaveCount(0);
    await expect(interaction(page)).toHaveCount(0);
    await expect(tail(page).getByText("Recent terminal output")).toBeVisible();
  });
});
