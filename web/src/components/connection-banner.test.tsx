import { describe, it, expect, afterEach } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { ConnectionBanner } from "./connection-banner";
import { seedStore } from "@/test/fixtures";

afterEach(() => {
  cleanup();
  act(() => seedStore({ connection: "live" }));
});

describe("ConnectionBanner", () => {
  it("renders nothing when live", () => {
    seedStore({ connection: "live" });
    const { container } = render(<ConnectionBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows a reconnecting bar in trouble", () => {
    seedStore({ connection: "trouble" });
    render(<ConnectionBanner />);
    expect(screen.getByRole("status")).toHaveTextContent(/reconnecting/i);
  });

  it("shows a lost bar with a Retry action", () => {
    seedStore({ connection: "lost" });
    render(<ConnectionBanner />);
    expect(screen.getByText(/connection lost/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /retry/i })).toBeInTheDocument();
  });
});
