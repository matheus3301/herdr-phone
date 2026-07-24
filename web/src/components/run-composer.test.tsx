import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RunComposer, type ComposerResult } from "./run-composer";
import { buildRuns } from "@/lib/run";
import { makeSnapshot } from "@/test/fixtures";

const run = buildRuns(makeSnapshot()).find((r) => r.agentName === "claude")!;

/** Wrap the controlled composer so the draft behaves as it does in the route. */
function Harness({
  onSubmit,
  disabled,
  disabledReason,
  initial = "",
}: {
  onSubmit: (text: string) => Promise<ComposerResult>;
  disabled?: boolean;
  disabledReason?: string;
  initial?: string;
}) {
  const [value, setValue] = useState(initial);
  return (
    <RunComposer
      run={run}
      value={value}
      onChange={setValue}
      onSubmit={onSubmit}
      pending={false}
      disabled={disabled}
      disabledReason={disabledReason}
    />
  );
}

const field = () => screen.getByLabelText(/instruction for claude/i);

describe("RunComposer — the target is always visible", () => {
  it("names the agent and its workspace before anything is sent", () => {
    render(<Harness onSubmit={async () => "accepted"} />);
    expect(screen.getByText(/claude · space-api \/ space-api-auth/)).toBeInTheDocument();
  });
});

describe("RunComposer — the draft survives failure", () => {
  it("clears only when the relay accepted the instruction", async () => {
    render(<Harness onSubmit={async () => "accepted"} initial="continue" />);
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));
    expect(field()).toHaveValue("");
  });

  it("keeps the text when the send was rejected", async () => {
    render(<Harness onSubmit={async () => "kept"} initial="continue" />);
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));
    expect(field()).toHaveValue("continue");
  });

  it("clears once delivery became uncertain, because the run now tracks it", async () => {
    // The instruction is not lost: it is listed in the run with an explicit
    // "send again" decision, so leaving it in the box would duplicate it.
    render(<Harness onSubmit={async () => "uncertain"} initial="deploy" />);
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));
    expect(field()).toHaveValue("");
  });

  it("keeps the text and explains itself while the link is down", async () => {
    const onSubmit = vi.fn();
    render(
      <Harness
        onSubmit={onSubmit}
        disabled
        disabledReason="Can't reach your Mac. Your draft is kept here until the link is back."
        initial="restart the tests"
      />,
    );
    expect(screen.getByText(/your draft is kept here/i)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(field()).toHaveValue("restart the tests");
  });
});

describe("RunComposer — keyboard and IME", () => {
  it("sends on Enter", async () => {
    const onSubmit = vi.fn(async () => "accepted" as ComposerResult);
    render(<Harness onSubmit={onSubmit} initial="go" />);
    await userEvent.type(field(), "{Enter}");
    expect(onSubmit).toHaveBeenCalledWith("go");
  });

  it("inserts a newline on Shift+Enter", async () => {
    const onSubmit = vi.fn(async () => "accepted" as ComposerResult);
    render(<Harness onSubmit={onSubmit} initial="one" />);
    await userEvent.type(field(), "{Shift>}{Enter}{/Shift}two");
    expect(onSubmit).not.toHaveBeenCalled();
    expect(field()).toHaveValue("one\ntwo");
  });

  it("does not send while an IME composition is open", async () => {
    const onSubmit = vi.fn(async () => "accepted" as ComposerResult);
    render(<Harness onSubmit={onSubmit} initial="こんにち" />);
    const input = field();
    await userEvent.click(input);
    // Enter commits the candidate here; stealing it would send half a word.
    input.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
    await userEvent.keyboard("{Enter}");
    expect(onSubmit).not.toHaveBeenCalled();

    input.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
    await userEvent.keyboard("{Enter}");
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  // The guard has two halves because browsers disagree: some report
  // `KeyboardEvent.isComposing`, and the composition-event pair covers the rest.
  // The test above exercises the ref; this one exercises the flag on its own, so
  // a browser that never fires compositionstart still cannot lose a candidate.
  it("does not send when the key event itself reports composing", async () => {
    const onSubmit = vi.fn(async () => "accepted" as ComposerResult);
    render(<Harness onSubmit={onSubmit} initial="こんにち" />);
    const input = field();
    input.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true, isComposing: true }),
    );
    expect(onSubmit).not.toHaveBeenCalled();

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });
});

describe("RunComposer — advisory danger confirmation", () => {
  it("requires a second, deliberate tap for a destructive-looking instruction", async () => {
    const onSubmit = vi.fn(async () => "accepted" as ComposerResult);
    render(<Harness onSubmit={onSubmit} initial="sudo rm -rf /tmp/build" />);

    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText(/send again to confirm/i)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /confirm and send instruction/i }));
    expect(onSubmit).toHaveBeenCalledWith("sudo rm -rf /tmp/build");
  });

  it("re-arms when the instruction is edited", async () => {
    const onSubmit = vi.fn(async () => "accepted" as ComposerResult);
    render(<Harness onSubmit={onSubmit} initial="sudo reboot" />);
    await userEvent.click(screen.getByRole("button", { name: /send instruction/i }));
    await userEvent.type(field(), " now");
    expect(screen.getByRole("button", { name: /^send instruction$/i })).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
