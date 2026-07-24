import { describe, it, expect, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TopologyRibbon } from "./topology-ribbon";
import { renderWithRouter } from "@/test/render";
import { seedStore, makeSnapshot } from "@/test/fixtures";
import { selectionStore } from "@/lib/selection";

describe("TopologyRibbon", () => {
  beforeEach(() => {
    seedStore({ snapshot: makeSnapshot(), connection: "live" });
    selectionStore.set("w1:p1");
  });

  it("renders the three topology layers with switchers", () => {
    renderWithRouter(<TopologyRibbon />);
    expect(screen.getByRole("button", { name: /open space switcher/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /open tab switcher/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /open pane switcher/i })).toBeInTheDocument();
  });

  it("marks the active workspace chip with aria-current", () => {
    renderWithRouter(<TopologyRibbon />);
    // The chip's accessible name includes its status-dot label, so match by substring.
    const active = screen.getByRole("button", { name: /space-api/ });
    expect(active).toHaveAttribute("aria-current", "true");
  });

  it("opens the workspace switcher with a create action", async () => {
    renderWithRouter(<TopologyRibbon />);
    await userEvent.click(screen.getByRole("button", { name: /open space switcher/i }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /new workspace/i })).toBeInTheDocument();
  });
});
