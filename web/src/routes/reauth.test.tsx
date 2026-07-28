import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ReauthScreen } from "./reauth";

describe("ReauthScreen", () => {
  it("names Access as the remedy and offers no pairing field", () => {
    render(<ReauthScreen error={null} onReload={() => {}} onUsePairing={() => {}} />);
    expect(screen.getByRole("heading", { name: /sign in to continue/i })).toBeInTheDocument();
    expect(screen.getByText(/cloudflare access/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/pairing link or secret/i)).not.toBeInTheDocument();
  });

  it("reloads on request — only a navigation can renew the Access identity", async () => {
    const onReload = vi.fn();
    render(<ReauthScreen error={null} onReload={onReload} onUsePairing={() => {}} />);
    await userEvent.click(screen.getByRole("button", { name: /reload and sign in/i }));
    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it("keeps a pairing escape hatch", async () => {
    const onUsePairing = vi.fn();
    render(<ReauthScreen error={null} onReload={() => {}} onUsePairing={onUsePairing} />);
    await userEvent.click(screen.getByRole("button", { name: /use a pairing link instead/i }));
    expect(onUsePairing).toHaveBeenCalledTimes(1);
  });

  it("surfaces the relay's reason with an alert role", () => {
    render(<ReauthScreen error="access denied" onReload={() => {}} onUsePairing={() => {}} />);
    expect(screen.getByRole("alert")).toHaveTextContent("access denied");
  });

  it("keeps the remote-shell warning", () => {
    render(<ReauthScreen error={null} onReload={() => {}} onUsePairing={() => {}} />);
    expect(screen.getByText(/remote shell-equivalent access/i)).toBeInTheDocument();
  });
});
