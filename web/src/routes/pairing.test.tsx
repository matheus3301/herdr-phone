import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { extractPairingSecret } from "@/lib/pairing";
import { PairingScreen } from "./pairing";

describe("extractPairingSecret", () => {
  it("pulls a secret from a full pairing URL fragment", () => {
    expect(extractPairingSecret("https://host/#pair=abc123")).toBe("abc123");
  });
  it("url-decodes and accepts a raw secret", () => {
    expect(extractPairingSecret("https://h/#pair=a%2Bb")).toBe("a+b");
    expect(extractPairingSecret("  raw-secret ")).toBe("raw-secret");
  });
});

describe("PairingScreen", () => {
  it("shows the remote-shell warning", () => {
    render(<PairingScreen onPair={() => {}} error={null} />);
    expect(screen.getByText(/remote shell-equivalent access/i)).toBeInTheDocument();
  });

  it("disables pairing until a secret is present, then pairs with the extracted secret", async () => {
    const onPair = vi.fn();
    render(<PairingScreen onPair={onPair} error={null} />);
    const button = screen.getByRole("button", { name: /pair device/i });
    expect(button).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/pairing link or secret/i), "https://host/#pair=xyz");
    expect(button).toBeEnabled();
    await userEvent.click(button);
    expect(onPair).toHaveBeenCalledWith("xyz");
  });

  it("renders an error message with an alert role", () => {
    render(<PairingScreen onPair={() => {}} error="Invalid or used pairing secret." />);
    expect(screen.getByRole("alert")).toHaveTextContent("Invalid or used pairing secret.");
  });
});
