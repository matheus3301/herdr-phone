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

  it("reconnects with a fresh, non-takeover controller after an unexpected close", () => {
    vi.useFakeTimers();
    try {
      const h = handlers();
      const s = new TerminalSocket("w1:p1", 80, 24, h);
      s.connect({ takeover: true, confirmation: "cnf", expectedGeneration: 2 });
      FakeWS.instances[0].open();
      FakeWS.instances[0].close(); // unexpected drop
      vi.advanceTimersByTime(2000);
      expect(FakeWS.instances.length).toBe(2);
      // The reconnect drops takeover intent and the single-use nonce.
      expect(FakeWS.instances[1].url).not.toContain("takeover=1");
      expect(FakeWS.instances[1].url).not.toContain("confirmation=");
    } finally {
      vi.useRealTimers();
    }
  });
});
