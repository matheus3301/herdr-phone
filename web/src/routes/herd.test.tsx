import { describe, it, expect, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HerdRoute } from "./herd";
import { renderWithRouter } from "@/test/render";
import { seedStore, makeSnapshot } from "@/test/fixtures";

describe("HerdRoute — blocked-first triage", () => {
  beforeEach(() => seedStore({ snapshot: makeSnapshot(), connection: "live" }));

  it("leads with Needs you, then Working, and collapses Quiet", () => {
    renderWithRouter(<HerdRoute />);
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings[0]).toMatch(/needs you/i);
    expect(headings[1]).toMatch(/working/i);
    // Quiet is a collapsible control, not an open heading.
    expect(screen.getByRole("button", { name: /quiet/i })).toHaveAttribute("aria-expanded", "false");
  });

  it("surfaces a blocked agent's question", () => {
    renderWithRouter(<HerdRoute />);
    expect(screen.getByText(/Approve this command\?/)).toBeInTheDocument();
  });

  it("expands the quiet group on demand", async () => {
    renderWithRouter(<HerdRoute />);
    await userEvent.click(screen.getByRole("button", { name: /quiet/i }));
    // codex is the quiet (done) agent in the fixture (name span + kind badge).
    expect(screen.getAllByText("codex").length).toBeGreaterThan(0);
  });

  it("offers an Open terminal action on the blocked agent", () => {
    renderWithRouter(<HerdRoute />);
    const blockedRegion = screen.getByText(/Approve this command\?/).closest("li")!;
    expect(within(blockedRegion).getByRole("button", { name: /open terminal/i })).toBeInTheDocument();
  });
});
