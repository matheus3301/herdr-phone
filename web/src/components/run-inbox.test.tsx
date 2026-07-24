import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { RunInbox } from "./run-inbox";
import { store } from "@/lib/store";
import * as api from "@/lib/api";
import { runSource } from "@/lib/run-source";
import {
  makeCapabilities,
  makeRunContract,
  makeSnapshot,
  makeWireRun,
  makeWireRunsResponse,
  seedStore,
} from "@/test/fixtures";
import type { Capabilities, Snapshot } from "@/lib/types";

function mount(snapshot: Snapshot | null = makeSnapshot(), capabilities: Capabilities = makeCapabilities()) {
  seedStore({ ready: true, snapshot, capabilities, connection: "live" });
  return render(
    <MemoryRouter>
      <RunInbox />
    </MemoryRouter>,
  );
}

/** Mount against a relay that advertises the structured run contract. */
function mountWithContract(snapshot: Snapshot | null = makeSnapshot()) {
  return mount(snapshot, makeCapabilities({ runs: makeRunContract() }));
}

beforeEach(() => {
  runSource.reset();
  vi.spyOn(store, "runMutation").mockResolvedValue({ request_id: "r", accepted: true, result: {} });
});
afterEach(() => {
  runSource.reset();
  vi.restoreAllMocks();
});

describe("RunInbox — attention-first sections", () => {
  it("lists sections in urgency order", () => {
    mount();
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings[0]).toMatch(/needs you/i);
    expect(headings.some((h) => /working/i.test(h ?? ""))).toBe(true);
  });

  it("labels settled background work as Updated, never as ready or successful", () => {
    mount();
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent ?? "");
    expect(headings.some((h) => /updated/i.test(h))).toBe(true);
    expect(headings.some((h) => /ready|successful/i.test(h))).toBe(false);
  });

  it("gives unknown its own section rather than folding it into idle", () => {
    const snapshot = makeSnapshot();
    snapshot.agents = snapshot.agents.map((a) => (a.name === "codex" ? { ...a, status: "unknown" as const } : a));
    mount(snapshot);
    const unknown = screen.getByRole("heading", { level: 2, name: /status unknown/i });
    const section = unknown.closest("section")!;
    expect(within(section).getAllByText("codex").length).toBeGreaterThan(0);
    expect(screen.queryByRole("heading", { level: 2, name: /^idle/i })).toBeNull();
  });

  it("shows agent identity, Herdr's pane title, and workspace context on a row", () => {
    mount();
    const attention = screen.getByRole("heading", { level: 2, name: /needs you/i }).closest("section")!;
    // The agent's name and its kind are both shown, so scope the assertion.
    expect(within(attention).getAllByText("claude")).toHaveLength(2);
    expect(within(attention).getByText("Approve this command?")).toBeInTheDocument();
    expect(within(attention).getByText(/space-api \/ auth-refactor/)).toBeInTheDocument();
  });

  it("links a row to its run without touching Herdr focus", async () => {
    mount();
    const link = screen.getAllByRole("link").find((a) => /\/runs\/w/.test(a.getAttribute("href") ?? ""))!;
    // The run id is the pane plus its generation, so the link is bound to one
    // incarnation.
    expect(link).toHaveAttribute("href", "/runs/w1%3Ap1~g3");
    await userEvent.click(link);
    expect(store.runMutation).not.toHaveBeenCalled();
  });
});

describe("RunInbox — production run mode", () => {
  it("lists the relay's runs and links them by their authoritative ids", async () => {
    const spy = vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse());
    mountWithContract();

    const link = await screen.findByRole("link", { name: /claude/ });
    expect(link).toHaveAttribute("href", "/runs/w1%3Ap1%403");
    expect(spy).toHaveBeenCalled();
  });

  it("honours the relay's list truncation instead of reading a short list as complete", async () => {
    vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse({ truncated: true }));
    mountWithContract();

    expect(await screen.findByText(/returned only the first 200 runs/i)).toBeInTheDocument();
    expect(screen.getByText(/some runs are not listed here/i)).toBeInTheDocument();
  });

  it("does not show a truncation notice for a complete list", async () => {
    vi.spyOn(api, "getRuns").mockResolvedValue(makeWireRunsResponse());
    mountWithContract();
    await screen.findByRole("link", { name: /claude/ });
    expect(screen.queryByText(/some runs are not listed here/i)).toBeNull();
  });

  it("keeps an unrecognised relay status out of idle and out of any completion state", async () => {
    vi.spyOn(api, "getRuns").mockResolvedValue(
      makeWireRunsResponse({ runs: [makeWireRun({ status: "reticulating" })] }),
    );
    mountWithContract();

    const unknown = await screen.findByRole("heading", { level: 2, name: /status unknown/i });
    expect(within(unknown.closest("section")!).getAllByText("claude").length).toBeGreaterThan(0);
    expect(screen.queryByRole("heading", { level: 2, name: /^idle/i })).toBeNull();
  });
});

describe("RunInbox — empty state", () => {
  it("offers the creation journey when nothing is running", () => {
    mount({ ...makeSnapshot(), agents: [] });
    expect(screen.getByText(/no agents are running/i)).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: /start run/i }).length).toBeGreaterThan(0);
  });
});

describe("RunInbox — announcements", () => {
  it("announces new attention once, and says nothing when there is none", () => {
    const calm = makeSnapshot();
    calm.agents = calm.agents.map((a) => ({ ...a, status: "idle" as const }));
    const { rerender } = mount(calm);
    const region = document.querySelector("[aria-live='polite']")!;
    expect(region.textContent).toBe("");

    seedStore({ snapshot: makeSnapshot() });
    rerender(
      <MemoryRouter>
        <RunInbox />
      </MemoryRouter>,
    );
    expect(region.textContent).toMatch(/1 run needs you/i);
  });
});
