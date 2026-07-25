import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Runline } from "./runline";
import type { ObservedEvent } from "@/lib/run-store";

const NOW = 1_000_000;

function event(overrides: Partial<ObservedEvent> = {}): ObservedEvent {
  return {
    id: "e1",
    kind: "status",
    text: "Agent started working",
    status: "working",
    at: NOW - 60_000,
    tone: "active",
    ...overrides,
  };
}

describe("Runline", () => {
  it("renders nothing when there is no observed activity", () => {
    const { container } = render(<Runline events={[]} now={NOW} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("is a semantic ordered list, so a screen reader gets a countable feed", () => {
    render(<Runline events={[event({ id: "a" }), event({ id: "b" })]} now={NOW} />);
    expect(screen.getByRole("list")).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
  });

  it("stamps each entry as locally observed rather than reported by Herdr", () => {
    render(<Runline events={[event()]} now={NOW} />);
    expect(screen.getByText(/^seen 1m ago$/)).toBeInTheDocument();
  });

  it("carries tone as data, not as the only cue", () => {
    render(
      <Runline
        events={[
          event({ id: "a", tone: "attention", text: "Agent stopped for a decision" }),
          event({ id: "b", tone: "settled", text: "Background work settled" }),
        ]}
        now={NOW}
      />,
    );
    const items = screen.getAllByRole("listitem");
    expect(items[0]).toHaveAttribute("data-tone", "attention");
    expect(items[1]).toHaveAttribute("data-tone", "settled");
    // Each entry also says what happened in words.
    expect(screen.getByText("Agent stopped for a decision")).toBeInTheDocument();
  });

  it("does not animate entries that were already present on the first paint", () => {
    render(<Runline events={[event({ id: "a" }), event({ id: "b" })]} now={NOW} />);
    for (const item of screen.getAllByRole("listitem")) {
      expect(item).not.toHaveAttribute("data-fresh");
    }
  });

  it("marks only a genuinely new entry as fresh", () => {
    const { rerender } = render(<Runline events={[event({ id: "a" })]} now={NOW} />);
    rerender(<Runline events={[event({ id: "a" }), event({ id: "b", text: "Agent went idle" })]} now={NOW} />);
    const items = screen.getAllByRole("listitem");
    expect(items[0]).not.toHaveAttribute("data-fresh");
    expect(items[1]).toHaveAttribute("data-fresh", "true");
  });
});
