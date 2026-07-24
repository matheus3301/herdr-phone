import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { KeyDock } from "./key-dock";

afterEach(() => vi.restoreAllMocks());

function setClipboard(impl: Partial<Clipboard>) {
  Object.defineProperty(navigator, "clipboard", { value: impl, configurable: true });
}

describe("KeyDock", () => {
  it("sends a bare key", async () => {
    const onChord = vi.fn();
    render(<KeyDock onChord={onChord} />);
    await userEvent.click(screen.getByRole("button", { name: "Enter" }));
    expect(onChord).toHaveBeenCalledWith("enter");
  });

  it("composes an armed tri-state modifier with the next key", async () => {
    const onChord = vi.fn();
    render(<KeyDock onChord={onChord} />);
    const ctrl = screen.getByRole("button", { name: /^Ctrl$/ });
    await userEvent.click(ctrl); // off -> next
    expect(ctrl).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(screen.getByRole("button", { name: "Tab" }));
    expect(onChord).toHaveBeenCalledWith("ctrl+tab");
    // one-shot modifier cleared after the key
    expect(ctrl).toHaveAttribute("aria-pressed", "false");
  });

  it("sends ctrl+c from the dedicated interrupt key", async () => {
    const onChord = vi.fn();
    render(<KeyDock onChord={onChord} />);
    await userEvent.click(screen.getByRole("button", { name: /send ctrl\+c interrupt/i }));
    expect(onChord).toHaveBeenCalledWith("ctrl+c");
  });

  it("forwards raw multi-line clipboard text (sanitized downstream, F1)", async () => {
    // Multi-line + tab must be preserved through the dock, never concatenated.
    setClipboard({ readText: vi.fn().mockResolvedValue("echo a\necho b\techo c") });
    const onPaste = vi.fn();
    render(<KeyDock onChord={vi.fn()} onPaste={onPaste} />);
    await userEvent.click(screen.getByRole("button", { name: /paste from clipboard/i }));
    await waitFor(() => expect(onPaste).toHaveBeenCalledWith("echo a\necho b\techo c"));
  });

  it("surfaces an explicit permission-denied error", async () => {
    setClipboard({ readText: vi.fn().mockRejectedValue(new DOMException("no", "NotAllowedError")) });
    render(<KeyDock onChord={vi.fn()} onPaste={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: /paste from clipboard/i }));
    expect(await screen.findByText(/permission denied/i)).toBeInTheDocument();
  });

  it("distinguishes a generic read failure from a denial", async () => {
    setClipboard({ readText: vi.fn().mockRejectedValue(new Error("boom")) });
    render(<KeyDock onChord={vi.fn()} onPaste={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: /paste from clipboard/i }));
    expect(await screen.findByText(/couldn't read the clipboard/i)).toBeInTheDocument();
  });

  it("reports an empty clipboard", async () => {
    setClipboard({ readText: vi.fn().mockResolvedValue("") });
    render(<KeyDock onChord={vi.fn()} onPaste={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: /paste from clipboard/i }));
    expect(await screen.findByText(/clipboard is empty/i)).toBeInTheDocument();
  });

  it("omits the paste control when no onPaste handler is given", () => {
    render(<KeyDock onChord={vi.fn()} />);
    expect(screen.queryByRole("button", { name: /paste from clipboard/i })).toBeNull();
  });
});
