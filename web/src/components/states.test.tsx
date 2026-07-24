import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { EmptyState, ErrorState, OfflineState } from "./states";

describe("empty/error/offline states", () => {
  it("empty state names a recovery action", () => {
    render(<EmptyState title="No panes yet" description="Create a workspace to continue." />);
    expect(screen.getByRole("status")).toBeInTheDocument();
    expect(screen.getByText("No panes yet")).toBeInTheDocument();
    expect(screen.getByText("Create a workspace to continue.")).toBeInTheDocument();
  });

  it("error state uses an alert role and fires its action", async () => {
    const onClick = vi.fn();
    render(<ErrorState title="Failed" description="Reload the app." action={{ label: "Reload", onClick }} />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("offline state renders its recovery action", () => {
    const onClick = vi.fn();
    render(<OfflineState title="Offline" description="Reconnect the tunnel." action={{ label: "Retry", onClick }} />);
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
