import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { TerminalSocket } from "./terminal-socket";

/** Controllable fake WebSocket capturing URL + sent frames. */
class FakeWS {
  static instances: FakeWS[] = [];
  static OPEN = 1;
  readonly OPEN = 1;
  url: string;
  readyState = 0;
  binaryType = "arraybuffer";
  sent: Array<string | ArrayBufferView> = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(url: string) {
    this.url = url;
    FakeWS.instances.push(this);
  }
  send(data: string | ArrayBufferView) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
    this.onclose?.();
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  message(data: unknown) {
    this.onmessage?.({ data });
  }
  textFrames() {
    return this.sent.filter((s) => typeof s === "string") as string[];
  }
}

beforeEach(() => {
  FakeWS.instances = [];
  vi.stubGlobal("WebSocket", FakeWS as unknown as typeof WebSocket);
  vi.stubGlobal("location", { protocol: "http:", host: "127.0.0.1:4173" } as unknown as Location);
});
afterEach(() => vi.unstubAllGlobals());

function handlers() {
  return { onData: vi.fn(), onControl: vi.fn(), onStatus: vi.fn() };
}

describe("TerminalSocket", () => {
  it("connects with cols/rows and reports open", () => {
    const h = handlers();
    const s = new TerminalSocket("w1:p1", 80, 24, h);
    s.connect({ expectedGeneration: 3 });
    const ws = FakeWS.instances[0];
    expect(ws.url).toContain("/terminals/w1%3Ap1?");
    expect(ws.url).toContain("cols=80");
    expect(ws.url).toContain("rows=24");
    expect(ws.url).toContain("expected_generation=3");
    ws.open();
    expect(h.onStatus).toHaveBeenCalledWith("open");
  });

  it("passes takeover + confirmation as query params", () => {
    const s = new TerminalSocket("w1:p1", 80, 24, handlers());
    s.connect({ takeover: true, confirmation: "cnf-x", expectedGeneration: 5 });
    const ws = FakeWS.instances[0];
    expect(ws.url).toContain("takeover=1");
    expect(ws.url).toContain("confirmation=cnf-x");
    expect(ws.url).toContain("expected_generation=5");
  });

  it("sends resize with cols/rows and cell geometry", () => {
    const s = new TerminalSocket("w1:p1", 80, 24, handlers());
    s.connect();
    FakeWS.instances[0].open();
    s.resize(100, 30, 8, 16);
    const frame = JSON.parse(FakeWS.instances[0].textFrames().at(-1)!);
    expect(frame).toEqual({ type: "resize", cols: 100, rows: 30, cell_width_px: 8, cell_height_px: 16 });
  });

  it("defaults cell geometry to 0 when unknown", () => {
    const s = new TerminalSocket("w1:p1", 80, 24, handlers());
    s.connect();
    FakeWS.instances[0].open();
    s.resize(90, 20);
    const frame = JSON.parse(FakeWS.instances[0].textFrames().at(-1)!);
    expect(frame.cell_width_px).toBe(0);
    expect(frame.cell_height_px).toBe(0);
  });

  it("sends scroll control frames", () => {
    const s = new TerminalSocket("w1:p1", 80, 24, handlers());
    s.connect();
    FakeWS.instances[0].open();
    s.scroll("up", 3, "wheel");
    expect(JSON.parse(FakeWS.instances[0].textFrames().at(-1)!)).toEqual({ type: "scroll", direction: "up", lines: 3, source: "wheel" });
  });

  it("delivers binary frames to onData and control frames to onControl", () => {
    const h = handlers();
    const s = new TerminalSocket("w1:p1", 80, 24, h);
    s.connect();
    const ws = FakeWS.instances[0];
    ws.open();
    ws.message(new TextEncoder().encode("hi").buffer);
    expect(h.onData).toHaveBeenCalled();
    ws.message(JSON.stringify({ type: "terminal.conflict", reason: "busy" }));
    expect(h.onStatus).toHaveBeenCalledWith("conflict");
    expect(h.onControl).toHaveBeenCalledWith(expect.objectContaining({ type: "terminal.conflict" }));
  });

  it("sends release on client close", () => {
    const s = new TerminalSocket("w1:p1", 80, 24, handlers());
    s.connect();
    FakeWS.instances[0].open();
    s.close();
    expect(JSON.parse(FakeWS.instances[0].textFrames().at(-1)!)).toEqual({ type: "release" });
  });

  it("reconnects with a fresh, non-takeover controller but keeps the generation", () => {
    vi.useFakeTimers();
    try {
      const h = handlers();
      const s = new TerminalSocket("w1:p1", 80, 24, h);
      s.connect({ takeover: true, confirmation: "cnf", expectedGeneration: 2 });
      FakeWS.instances[0].open();
      FakeWS.instances[0].close(); // unexpected drop
      vi.advanceTimersByTime(2000);
      expect(FakeWS.instances.length).toBe(2);
      // The reconnect drops takeover intent and the single-use nonce…
      expect(FakeWS.instances[1].url).not.toContain("takeover=1");
      expect(FakeWS.instances[1].url).not.toContain("confirmation=");
      // …but attach is generation-checked and the assertion is mandatory, so
      // dropping it would make every reconnect fail (or, worse, be ambiguous
      // about which pane incarnation it landed on).
      expect(FakeWS.instances[1].url).toContain("expected_generation=2");
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps the generation across repeated reconnects", () => {
    vi.useFakeTimers();
    try {
      const s = new TerminalSocket("w1:p1", 80, 24, handlers());
      s.connect({ expectedGeneration: 7 });
      for (let attempt = 0; attempt < 3; attempt++) {
        FakeWS.instances.at(-1)!.open();
        FakeWS.instances.at(-1)!.close();
        vi.advanceTimersByTime(10_000);
      }
      expect(FakeWS.instances.length).toBe(4);
      for (const ws of FakeWS.instances) expect(ws.url).toContain("expected_generation=7");
    } finally {
      vi.useRealTimers();
    }
  });
});

/**
 * Review MEDIUM 2.
 *
 * A stale generation is refused with HTTP 409 *before* the WebSocket upgrade, and
 * the browser exposes a failed handshake only as onerror → onclose with no status
 * code. So the socket used to re-dial a permanently invalid `expected_generation`
 * forever, backoff-capped at 8s, with the UI stuck on "Reattaching…" and no way
 * to tell "the link dropped" from "the pane you were on no longer exists".
 */
describe("TerminalSocket — a handshake that can never succeed", () => {
  /** A liveness probe over a mutable snapshot stand-in. */
  function liveness(generations: Record<string, number> | null) {
    return {
      revalidate: vi.fn(),
      generationOf: (id: string) => (generations === null ? null : (generations[id] ?? 0)),
      set: (next: Record<string, number> | null) => {
        generations = next;
      },
    };
  }

  it("stops retrying and names the replacement once a snapshot proves it", () => {
    vi.useFakeTimers();
    try {
      const h = handlers();
      // The pane still exists but now belongs to a different incarnation.
      const live = liveness({ "w1:p1": 4 });
      const s = new TerminalSocket("w1:p1", 80, 24, h, live);
      s.connect({ expectedGeneration: 3 });
      // The upgrade is refused: onclose without ever having opened.
      FakeWS.instances[0].close();
      expect(live.revalidate).toHaveBeenCalled();
      vi.advanceTimersByTime(60_000);

      expect(FakeWS.instances.length, "no further dials").toBe(1);
      expect(h.onStatus).toHaveBeenCalledWith("pane-replaced");
      expect(h.onStatus).not.toHaveBeenCalledWith("open");
      expect(s.isInvalidated()).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it("distinguishes a pane that is gone entirely from one that was recycled", () => {
    vi.useFakeTimers();
    try {
      const h = handlers();
      // Generation 0: the pane itself is absent from the snapshot.
      const s = new TerminalSocket("w1:p1", 80, 24, h, liveness({}));
      s.connect({ expectedGeneration: 3 });
      FakeWS.instances[0].close();
      vi.advanceTimersByTime(60_000);

      expect(FakeWS.instances.length).toBe(1);
      expect(h.onStatus).toHaveBeenCalledWith("agent-ended");
      expect(h.onStatus).not.toHaveBeenCalledWith("pane-replaced");
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps retrying when the snapshot has not landed — offline is not proof", () => {
    vi.useFakeTimers();
    try {
      const h = handlers();
      // null: no snapshot at all, which is exactly the phone-offline case. The
      // poll is failing too, so absence must never be read as "the pane is gone".
      const s = new TerminalSocket("w1:p1", 80, 24, h, liveness(null));
      s.connect({ expectedGeneration: 3 });
      FakeWS.instances[0].close();
      vi.advanceTimersByTime(60_000);

      expect(FakeWS.instances.length, "ordinary reconnect preserved").toBeGreaterThan(1);
      expect(h.onStatus).toHaveBeenCalledWith("reconnecting");
      expect(h.onStatus).not.toHaveBeenCalledWith("pane-replaced");
      expect(h.onStatus).not.toHaveBeenCalledWith("agent-ended");
      expect(s.isInvalidated()).toBe(false);
      for (const ws of FakeWS.instances) expect(ws.url).toContain("expected_generation=3");
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps retrying when the generation still matches — a transient drop", () => {
    vi.useFakeTimers();
    try {
      const h = handlers();
      const s = new TerminalSocket("w1:p1", 80, 24, h, liveness({ "w1:p1": 3 }));
      s.connect({ expectedGeneration: 3 });
      FakeWS.instances[0].close();
      vi.advanceTimersByTime(60_000);

      expect(FakeWS.instances.length).toBeGreaterThan(1);
      expect(h.onStatus).not.toHaveBeenCalledWith("pane-replaced");
    } finally {
      vi.useRealTimers();
    }
  });

  it("never abandons a socket that had opened — that is an ordinary drop", () => {
    vi.useFakeTimers();
    try {
      const h = handlers();
      // The generation moved on, but this socket *was* attached: the drop is a
      // real disconnect and the route's own remount handles the replacement.
      const s = new TerminalSocket("w1:p1", 80, 24, h, liveness({ "w1:p1": 9 }));
      s.connect({ expectedGeneration: 3 });
      FakeWS.instances[0].open();
      FakeWS.instances[0].close();
      vi.advanceTimersByTime(60_000);

      expect(FakeWS.instances.length).toBeGreaterThan(1);
      expect(h.onStatus).not.toHaveBeenCalledWith("pane-replaced");
    } finally {
      vi.useRealTimers();
    }
  });
});
