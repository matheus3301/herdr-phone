import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { TerminalTail } from "./terminal-tail";
import * as api from "@/lib/api";
import { ApiError } from "@/lib/api";
import { buildRuns } from "@/lib/run";
import {
  makeCapabilities,
  makeRunContract,
  makeSnapshot,
  makeWireRun,
  makeWireRunCapabilities,
  seedStore,
} from "@/test/fixtures";
import type { WireObservedOutputPart } from "@/lib/types";

/**
 * Recent terminal output.
 *
 * This component owns the single most important labelling rule in the product:
 * bytes a pane rendered are presented as bytes a pane rendered, never as an
 * agent's own message. It also owns the unknown-part-type handling, which is how
 * the client stays honest when a future relay adds a part this build cannot
 * interpret. Both were shipping untested (review LOW 5).
 */

const run = buildRuns(makeSnapshot()).find((r) => r.agentName === "claude")!;

function part(overrides: Partial<WireObservedOutputPart> = {}): WireObservedOutputPart {
  return {
    type: "observed_terminal_output",
    source: "recent-unwrapped",
    format: "text",
    lines: 3,
    bytes: 24,
    truncated: false,
    text: "$ go test ./...\nok\n",
    ...overrides,
  };
}

function mount(opts: { contract?: boolean } = {}) {
  seedStore({
    ready: true,
    snapshot: makeSnapshot(),
    capabilities: makeCapabilities(opts.contract === false ? {} : { runs: makeRunContract() }),
    connection: "live",
  });
  return render(
    <MemoryRouter>
      <TerminalTail run={run} now={Date.now()} />
    </MemoryRouter>,
  );
}

function respond(parts: WireObservedOutputPart[]) {
  return vi.spyOn(api, "getRun").mockResolvedValue({
    contract_version: 1,
    capabilities: makeWireRunCapabilities(),
    run: makeWireRun(),
    parts,
  });
}

describe("TerminalTail — never a transcript, never a message", () => {
  beforeEach(() => {
    vi.spyOn(api, "readPane").mockResolvedValue({
      pane_id: run.paneId,
      source: "recent",
      lines: 2,
      content: "$ ls\nREADME.md\n",
    });
  });
  afterEach(() => vi.restoreAllMocks());

  it("labels contract output as terminal output and disclaims agent messages", async () => {
    respond([part()]);
    mount();
    expect(await screen.findByText(/this pane rendered/i)).toBeInTheDocument();
    expect(screen.getByText(/not a transcript/i)).toBeInTheDocument();
    expect(screen.getByText(/not the agent's own messages/i)).toBeInTheDocument();
  });

  it("renders the bytes into a pre, not as prose", async () => {
    respond([part({ text: "$ go build ./...\n" })]);
    mount();
    const block = await screen.findByText(/go build/);
    expect(block.tagName).toBe("PRE");
  });

  it("reports the line count the relay actually returned", async () => {
    // Review LOW 2: `lines` is what came back, so the copy is a true statement.
    respond([part({ lines: 3 })]);
    mount();
    expect(await screen.findByText(/the last 3 lines this pane rendered/i)).toBeInTheDocument();
  });

  it("says when the relay dropped older output to fit its bound", async () => {
    respond([part({ truncated: true })]);
    mount();
    expect(await screen.findByText(/older output was dropped/i)).toBeInTheDocument();
    expect(screen.getByText(/the console has the full scrollback/i)).toBeInTheDocument();
  });

  it("counts an unknown part type and refuses to render it", async () => {
    respond([part(), { ...part(), type: "assistant_message", text: "I have fixed the bug." }]);
    mount();
    expect(await screen.findByText(/1 part this app does not understand was not shown/i)).toBeInTheDocument();
    // The unknown part's body must not reach the DOM at all: interpreting it
    // would be exactly the fabricated conversation the design forbids.
    expect(screen.queryByText(/i have fixed the bug/i)).not.toBeInTheDocument();
  });

  it("renders nothing at all when the only parts are unknown", async () => {
    respond([{ ...part(), type: "structured_diff", text: "--- a/x\n+++ b/x\n" }]);
    mount();
    expect(await screen.findByText(/this pane has rendered nothing recently/i)).toBeInTheDocument();
    expect(screen.queryByText(/\+\+\+ b\/x/)).not.toBeInTheDocument();
  });

  it("shows a static message for a read failure, never the relay's own text", async () => {
    vi.spyOn(api, "getRun").mockRejectedValue(new ApiError(500, "run_read_failed", "cwd /secret/path failed"));
    mount();
    expect(await screen.findByText(/herdr could not read this pane/i)).toBeInTheDocument();
    expect(screen.queryByText(/secret\/path/)).not.toBeInTheDocument();
  });

  it("is explicitly silent to a screen reader, so a refresh is never announced", async () => {
    respond([part()]);
    const { container } = mount();
    await screen.findByText(/this pane rendered/i);
    const section = container.querySelector("section[aria-labelledby='recent-output-heading']");
    expect(section).toHaveAttribute("aria-live", "off");
  });

  it("keeps the console one tap away", async () => {
    respond([part()]);
    mount();
    const link = await screen.findByRole("link", { name: /open console/i });
    expect(link).toHaveAttribute("href", `/console/${encodeURIComponent(run.paneId)}?generation=${run.generation}`);
  });
});

describe("TerminalTail — the older-relay fallback", () => {
  afterEach(() => vi.restoreAllMocks());

  it("labels a bounded pane read the same way, without claiming a contract", async () => {
    vi.spyOn(api, "readPane").mockResolvedValue({
      pane_id: run.paneId,
      source: "recent",
      lines: 2,
      content: "$ ls\nREADME.md\n",
    });
    const getRun = vi.spyOn(api, "getRun");
    mount({ contract: false });

    expect(await screen.findByText(/README\.md/)).toBeInTheDocument();
    expect(screen.getByText(/not a transcript/i)).toBeInTheDocument();
    // Fails closed to `pane.read`: the contract route is never probed.
    expect(getRun).not.toHaveBeenCalled();
  });
});
