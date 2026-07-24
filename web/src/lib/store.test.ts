import { describe, it, expect, vi, afterEach } from "vitest";
import { AppStore } from "./store";
import * as api from "./api";
import { makePairResponse, makeSessionResponse } from "@/test/fixtures";

/** Minimal non-opening WebSocket stub so start() can run under jsdom. */
class IdleWS {
  static OPEN = 1;
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  close() {}
  send() {}
}

/** Controllable WebSocket stub: open()/drop() drive the lifecycle in tests. */
class ControllableWS {
  static OPEN = 1;
  static instances: ControllableWS[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor() {
    ControllableWS.instances.push(this);
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  drop() {
    this.readyState = 3;
    this.onclose?.();
  }
  close() {
    this.readyState = 3;
  }
  send() {}
}

function stubCaps() {
  vi.spyOn(api, "getCapabilities").mockResolvedValue({
    version: 1,
    operations: [],
    capabilities: { herdr_version: "0.7.5", herdr_protocol: 17, live_handoff: true, agent_kinds: [] },
    status: { version: "0.1.0", protocol: 17, mode: "quick", ready: true, herdr: { healthy: true }, state: { healthy: true }, clients: 1 },
    tunnel: { mode: "quick", public_url: "", health: { healthy: true } },
    limits: { max_body_bytes: 1, max_pane_read_lines: 1, confirmation_ttl_seconds: 30 },
  });
}

describe("AppStore", () => {
  it("starts disconnected with a stable getState reference", () => {
    const s = new AppStore(() => 0);
    const a = s.getState();
    const b = s.getState();
    expect(a).toBe(b);
    expect(a.connection).toBe("connecting");
    expect(a.snapshot).toBeNull();
  });

  it("notifies subscribers on session change and preserves immutability", () => {
    const s = new AppStore(() => 0);
    const cb = vi.fn();
    const off = s.subscribe(cb);
    const before = s.getState();
    s.setSessionFromPair(makePairResponse());
    const after = s.getState();
    expect(cb).toHaveBeenCalled();
    expect(after).not.toBe(before);
    expect(after.session?.operator).toBe("Quick Tunnel operator");
    expect(after.session?.csrfToken).toBe("csrf");
    off();
  });

  it("unsubscribes cleanly", () => {
    const s = new AppStore(() => 0);
    const cb = vi.fn();
    const off = s.subscribe(cb);
    off();
    s.setSessionFromPair(makePairResponse());
    expect(cb).not.toHaveBeenCalled();
  });
});

describe("AppStore.start — cold reload session recovery", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("recovers a mutable session (CSRF + expiry) from GET /session without re-pairing", async () => {
    vi.stubGlobal("WebSocket", IdleWS as unknown as typeof WebSocket);
    vi.stubGlobal("location", { protocol: "http:", host: "127.0.0.1:4173" } as unknown as Location);
    vi.spyOn(api, "getSession").mockResolvedValue(makeSessionResponse({ csrf_token: "reloaded-csrf", expires_unix_ms: 1_780_000_500_000 }));
    vi.spyOn(api, "getCapabilities").mockResolvedValue({
      version: 1,
      operations: ["pane.split"],
      capabilities: { herdr_version: "0.7.5", herdr_protocol: 17, live_handoff: true, agent_kinds: ["claude"] },
      status: { version: "0.1.0", protocol: 17, mode: "quick", ready: true, herdr: { healthy: true }, state: { healthy: true }, clients: 1 },
      tunnel: { mode: "quick", public_url: "", health: { healthy: true } },
      limits: { max_body_bytes: 1, max_pane_read_lines: 1, confirmation_ttl_seconds: 30 },
    });

    const s = new AppStore();
    await s.start();

    const st = s.getState();
    expect(st.session?.csrfToken).toBe("reloaded-csrf");
    expect(st.session?.expiresUnixMs).toBe(1_780_000_500_000);
    expect(st.readOnly).toBe(false);
    expect(s.canMutate()).toBe(true);
    s.stop();
  });

  it("stays 'connecting' (not 'lost') while the first /events socket is opening", async () => {
    vi.useFakeTimers();
    try {
      vi.stubGlobal("WebSocket", IdleWS as unknown as typeof WebSocket); // never fires onopen
      vi.stubGlobal("location", { protocol: "http:", host: "127.0.0.1:4173" } as unknown as Location);
      vi.spyOn(api, "getSession").mockResolvedValue(makeSessionResponse());
      vi.spyOn(api, "getCapabilities").mockRejectedValue(new Error("caps skipped"));

      let clock = 1_000_000;
      const s = new AppStore(() => clock);
      s.subscribe(() => {}); // starts the 1s health clock
      await s.start();

      // 5s elapse with the socket still opening: within grace → connecting, no alarm.
      clock += 5000;
      await vi.advanceTimersByTimeAsync(1000);
      expect(s.getState().connection).toBe("connecting");

      // Past the lost threshold with no live signal → a genuine failure surfaces.
      clock += 9000;
      await vi.advanceTimersByTimeAsync(1000);
      expect(s.getState().connection).toBe("lost");
      s.stop();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("AppStore connection health — readyState is authoritative liveness", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("keeps an idle OPEN socket 'live' well beyond LOST_MS (no application messages)", async () => {
    vi.useFakeTimers();
    try {
      ControllableWS.instances = [];
      vi.stubGlobal("WebSocket", ControllableWS as unknown as typeof WebSocket);
      vi.stubGlobal("location", { protocol: "http:", host: "127.0.0.1:4173" } as unknown as Location);
      vi.spyOn(api, "getSession").mockResolvedValue(makeSessionResponse());
      stubCaps();

      let clock = 1_000_000;
      const s = new AppStore(() => clock);
      s.subscribe(() => {});
      await s.start();
      ControllableWS.instances[0].open(); // socket opens; no further app messages

      // 60s of pure idle — far past LOST_MS (12s) — with zero messages.
      for (let i = 0; i < 60; i++) {
        clock += 1000;
        await vi.advanceTimersByTimeAsync(1000);
      }
      expect(s.getState().connection).toBe("live");
      s.stop();
    } finally {
      vi.useRealTimers();
    }
  });

  it("transitions to trouble then lost after the socket actually closes", async () => {
    vi.useFakeTimers();
    try {
      ControllableWS.instances = [];
      vi.stubGlobal("WebSocket", ControllableWS as unknown as typeof WebSocket);
      vi.stubGlobal("location", { protocol: "http:", host: "127.0.0.1:4173" } as unknown as Location);
      vi.spyOn(api, "getSession").mockResolvedValue(makeSessionResponse());
      vi.spyOn(api, "getSnapshot").mockRejectedValue(new Error("offline")); // poll fallback fails
      stubCaps();

      let clock = 2_000_000;
      const s = new AppStore(() => clock);
      s.subscribe(() => {});
      await s.start();
      ControllableWS.instances[0].open();
      clock += 1000;
      await vi.advanceTimersByTimeAsync(1000);
      expect(s.getState().connection).toBe("live");

      // The socket drops (server closed a dead peer / network gone).
      ControllableWS.instances[0].drop();

      // Elapsed since the drop drives trouble → lost (reconnect attempts stay
      // in CONNECTING, so they are never counted as live).
      clock += 5000;
      await vi.advanceTimersByTimeAsync(1000);
      expect(s.getState().connection).toBe("trouble");

      clock += 8000;
      await vi.advanceTimersByTimeAsync(1000);
      expect(s.getState().connection).toBe("lost");
      s.stop();
    } finally {
      vi.useRealTimers();
    }
  });
});
