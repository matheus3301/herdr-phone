import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Composer } from "./composer";

describe("Composer", () => {
  it("sends typed text and clears", async () => {
    const onSubmit = vi.fn();
    render(<Composer onSubmit={onSubmit} />);
    const box = screen.getByLabelText("Message or command");
    await userEvent.type(box, "ls -la");
    await userEvent.click(screen.getByRole("button", { name: /^send$/i }));
    expect(onSubmit).toHaveBeenCalledWith("ls -la");
    expect(box).toHaveValue("");
  });

  it("requires a second tap for a danger-pattern command", async () => {
    const onSubmit = vi.fn();
    render(<Composer onSubmit={onSubmit} />);
    await userEvent.type(screen.getByLabelText("Message or command"), "rm -rf build");
    expect(screen.getByText(/danger/i)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^send$/i }));
    expect(onSubmit).not.toHaveBeenCalled(); // first tap only arms
    await userEvent.click(screen.getByRole("button", { name: /confirm send/i }));
    expect(onSubmit).toHaveBeenCalledWith("rm -rf build");
  });
});
