import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { StatusDot } from "./status-dot";

describe("StatusDot", () => {
  it("exposes an accessible label per status", () => {
    render(<StatusDot status="blocked" />);
    expect(screen.getByRole("img", { name: "Needs you" })).toBeInTheDocument();
  });

  it("labels a working agent", () => {
    render(<StatusDot status="working" />);
    expect(screen.getByRole("img", { name: "Working" })).toBeInTheDocument();
  });
});
