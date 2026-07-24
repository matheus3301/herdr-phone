import { describe, it, expect } from "vitest";
import { normalizeSnapshot, normalizeCapabilities, sessionFromResponse } from "./normalize";
import { makeWireEnvelope, makePairResponse } from "@/test/fixtures";
import type { WireCapabilities } from "./types";

describe("normalizeSnapshot", () => {
  it("flattens the nested wire envelope into the view model", () => {
    const snap = normalizeSnapshot(makeWireEnvelope())!;
    expect(snap.hash).toBe("h7");
    expect(snap.herdrVersion).toBe("0.7.5");
    expect(snap.focusedPaneId).toBe("w1:p1");
    expect(snap.workspaces[0].id).toBe("w1");
  });

  it("derives pane generation from the generations map", () => {
    const snap = normalizeSnapshot(makeWireEnvelope())!;
    expect(snap.panes.find((p) => p.id === "w1:p1")?.generation).toBe(3);
    expect(snap.panes.find((p) => p.id === "w1:p2")?.generation).toBe(1);
  });

  it("derives pane zoom state from the tab layout", () => {
    const snap = normalizeSnapshot(makeWireEnvelope())!;
    // layout zoomed=true, focused_pane_id=w1:p1 → only p1 is zoomed.
    expect(snap.panes.find((p) => p.id === "w1:p1")?.zoomed).toBe(true);
    expect(snap.panes.find((p) => p.id === "w1:p2")?.zoomed).toBe(false);
  });

  it("derives tab.active from the workspace active_tab_id", () => {
    const snap = normalizeSnapshot(makeWireEnvelope())!;
    expect(snap.tabs[0].active).toBe(true);
  });

  it("derives workspace worktree provenance by open_workspace_id", () => {
    const snap = normalizeSnapshot(makeWireEnvelope())!;
    expect(snap.workspaces[0].worktree?.branch).toBe("auth-refactor");
  });

  it("maps agent kind/name/title/seq from the wire agent", () => {
    const snap = normalizeSnapshot(makeWireEnvelope())!;
    const a = snap.agents[0];
    expect(a.kind).toBe("claude");
    expect(a.title).toBe("Approve this command?");
    expect(a.stateChangeSeq).toBe(30);
  });

  it("returns null when topology is absent", () => {
    expect(normalizeSnapshot(makeWireEnvelope({ data: null }))).toBeNull();
  });
});

describe("normalizeCapabilities", () => {
  const wire: WireCapabilities = {
    version: 1,
    operations: ["pane.split", "worktree.remove_force"],
    capabilities: { herdr_version: "0.7.5", herdr_protocol: 17, live_handoff: true, agent_kinds: ["claude", "codex"] },
    status: { version: "0.1.0", protocol: 17, mode: "named", ready: true, herdr: { healthy: true }, state: { healthy: true }, clients: 2 },
    tunnel: { mode: "named", public_url: "https://x.example.com", health: { healthy: true } },
    limits: { max_body_bytes: 1, max_pane_read_lines: 2, confirmation_ttl_seconds: 30 },
  };

  it("maps agent kinds, mode, and access enforcement", () => {
    const c = normalizeCapabilities(wire, "0.1.0");
    expect(c.agentKinds).toEqual(["claude", "codex"]);
    expect(c.agentKindsAvailable).toBe(true);
    expect(c.mode).toBe("named");
    expect(c.accessEnforced).toBe(true);
    expect(c.herdrVersion).toBe("0.7.5");
  });

  it("flags agent kinds unavailable when the backend omits them", () => {
    const c = normalizeCapabilities({ ...wire, capabilities: { herdr_version: "0.7.5", herdr_protocol: 17, live_handoff: true, agent_kinds_error: "unavailable" } }, "0.1.0");
    expect(c.agentKindsAvailable).toBe(false);
    expect(c.agentKinds).toEqual([]);
  });
});

describe("session normalization", () => {
  it("carries the CSRF token + expiry from a pair response", () => {
    const s = sessionFromResponse(makePairResponse());
    expect(s.csrfToken).toBe("csrf");
    expect(s.operator).toBe("Quick Tunnel operator");
    expect(s.expiresUnixMs).toBeGreaterThan(0);
  });

  it("recovers the CSRF token from a GET /session response (cold reload)", () => {
    // GET /session now returns the same shape as pairing.
    const s = sessionFromResponse({
      csrf_token: "reloaded-csrf",
      expires_unix_ms: 1_780_000_000_000,
      identity: { subject: "me@example.com", display: "Me", quick: false, mode: "named" },
    });
    expect(s.csrfToken).toBe("reloaded-csrf");
    expect(s.expiresUnixMs).toBe(1_780_000_000_000);
    expect(s.mode).toBe("named");
    expect(s.operator).toBe("Me");
  });
});
