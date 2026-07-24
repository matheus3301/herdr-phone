/**
 * Terminal WebSocket client (SPEC §13) — reconciled to internal/terminal and
 * internal/server/terminalroute.go. Binary frames from the relay are decoded
 * terminal bytes handed straight to xterm.js (never innerHTML). Browser binary
 * frames are terminal input bytes; text frames are typed control commands
 * ("resize" / "scroll" / "release" / "ping"). Takeover requires a scoped
 * `terminal.takeover` confirmation nonce plus expected_generation query params.
 */
import { API_BASE } from "./api";
import { Backoff } from "./reconnect";
import type { TerminalClientControl, TerminalServerControl } from "./types";

/**
 * `pane-replaced` and `agent-ended` are terminal: the attach can never succeed
 * again with the generation this socket holds, so retrying is pointless and the
 * "Reattaching…" spinner would lie forever.
 */
export type TerminalStatus =
  | "connecting"
  | "open"
  | "conflict"
  | "closed"
  | "reconnecting"
  | "pane-replaced"
  | "agent-ended";

export interface TerminalSocketHandlers {
  onData: (bytes: Uint8Array) => void;
  onControl: (msg: TerminalServerControl) => void;
  onStatus: (status: TerminalStatus) => void;
}

export interface TerminalConnectOptions {
  takeover?: boolean;
  confirmation?: string;
  expectedGeneration?: number;
}

/**
 * How the socket learns whether its pane incarnation still exists.
 *
 * A stale generation is rejected with HTTP 409 *before* the WebSocket upgrade,
 * and the browser surfaces a failed handshake only as `onerror` → `onclose` with
 * no status code. So the socket cannot tell "the link dropped" from "this pane
 * belongs to someone else now" on its own — it has to ask the snapshot.
 */
export interface PaneLiveness {
  /** Force a fresh snapshot read. */
  revalidate: () => void;
  /**
   * The pane's current lifecycle generation, or 0 when the pane is gone. Null
   * when no snapshot has landed yet, which must NOT be read as "gone".
   */
  generationOf: (paneId: string) => number | null;
}

function wsUrl(path: string): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}${API_BASE}${path}`;
}

export class TerminalSocket {
  private ws: WebSocket | null = null;
  private backoff = new Backoff({ baseMs: 400, maxMs: 8000 });
  private closedByClient = false;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private connectOpts: TerminalConnectOptions = {};
  /** True once this socket's pane incarnation is provably gone. */
  private invalidated = false;
  /** True while the current socket has never reached `onopen`. */
  private everOpened = false;

  constructor(
    private readonly paneId: string,
    private cols: number,
    private rows: number,
    private readonly handlers: TerminalSocketHandlers,
    private readonly liveness?: PaneLiveness,
  ) {}

  connect(opts: TerminalConnectOptions = {}): void {
    this.connectOpts = opts;
    this.closedByClient = false;
    this.invalidated = false;
    this.open();
  }

  private open() {
    const q = new URLSearchParams({ cols: String(this.cols), rows: String(this.rows) });
    if (this.connectOpts.takeover) {
      q.set("takeover", "1");
      if (this.connectOpts.confirmation) q.set("confirmation", this.connectOpts.confirmation);
    }
    if (this.connectOpts.expectedGeneration && this.connectOpts.expectedGeneration > 0) {
      q.set("expected_generation", String(this.connectOpts.expectedGeneration));
    }
    this.handlers.onStatus(this.backoff.attempts > 0 ? "reconnecting" : "connecting");
    let ws: WebSocket;
    try {
      ws = new WebSocket(wsUrl(`/terminals/${encodeURIComponent(this.paneId)}?${q.toString()}`));
    } catch {
      this.scheduleReconnect();
      return;
    }
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    this.everOpened = false;

    ws.onopen = () => {
      this.everOpened = true;
      this.backoff.reset();
      this.handlers.onStatus("open");
      this.startPing();
    };
    ws.onmessage = (ev) => {
      if (typeof ev.data === "string") {
        try {
          const msg = JSON.parse(ev.data) as TerminalServerControl;
          if (msg.type === "terminal.conflict") this.handlers.onStatus("conflict");
          this.handlers.onControl(msg);
        } catch {
          /* ignore malformed control frame */
        }
        return;
      }
      this.handlers.onData(new Uint8Array(ev.data as ArrayBuffer));
    };
    ws.onclose = () => {
      this.stopPing();
      if (this.ws === ws) this.ws = null;
      if (this.closedByClient) {
        this.handlers.onStatus("closed");
        return;
      }
      this.scheduleReconnect();
    };
    ws.onerror = () => ws.close();
  }

  private scheduleReconnect() {
    // A reconnect starts a fresh, non-takeover controller: the takeover nonce
    // was single-use and the new controller reconstructs the screen from its
    // first full frame. The generation guard is NOT dropped — attach is
    // generation-checked and a missing expected_generation is rejected outright
    // (internal/server/terminalroute.go), so carrying it forward is what makes a
    // reconnect land on the same pane incarnation instead of failing or, worse,
    // attaching to a recycled pane.
    const handshakeFailed = !this.everOpened;
    this.connectOpts = { expectedGeneration: this.connectOpts.expectedGeneration };
    this.handlers.onStatus("reconnecting");
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    // A handshake that never opened may have been refused for good: the pre-upgrade
    // generation guard answers 409 and the browser hides the status code. Ask the
    // snapshot before spending another attempt, but only ever *stop* on proof —
    // an absent or unchanged snapshot keeps the ordinary reconnect behaviour, so a
    // phone that is merely offline still retries with backoff.
    if (handshakeFailed) this.liveness?.revalidate();
    this.reconnectTimer = setTimeout(() => {
      if (this.closedByClient) return;
      if (handshakeFailed && this.stopIfPaneMovedOn()) return;
      this.open();
    }, this.backoff.next());
  }

  /**
   * Report a permanently invalid attach, if the snapshot proves one.
   *
   * Returns true when retrying was abandoned. Silence — no snapshot yet, or a
   * generation that still matches — returns false and the caller retries.
   */
  private stopIfPaneMovedOn(): boolean {
    const expected = this.connectOpts.expectedGeneration ?? 0;
    if (!this.liveness || expected <= 0) return false;
    const current = this.liveness.generationOf(this.paneId);
    if (current === null) return false; // no fresh snapshot: not proof of anything
    if (current === expected) return false; // still the same incarnation: keep trying
    this.invalidated = true;
    this.stopPing();
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    // Generation 0 means the pane itself is gone; anything else means it was
    // recycled and now belongs to a different occupant.
    this.handlers.onStatus(current === 0 ? "agent-ended" : "pane-replaced");
    return true;
  }

  /** True once this socket's pane incarnation is provably gone. */
  isInvalidated(): boolean {
    return this.invalidated;
  }

  sendInput(data: Uint8Array | string): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    if (typeof data === "string") this.ws.send(new TextEncoder().encode(data));
    else this.ws.send(data);
  }

  private sendControl(msg: TerminalClientControl): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    this.ws.send(JSON.stringify(msg));
  }

  /** Resize with cols/rows and cell geometry. cellWidthPx/cellHeightPx default to
   * 0 when the rendered geometry isn't measurable yet (documented fallback). */
  resize(cols: number, rows: number, cellWidthPx = 0, cellHeightPx = 0): void {
    this.cols = cols;
    this.rows = rows;
    this.sendControl({ type: "resize", cols, rows, cell_width_px: cellWidthPx, cell_height_px: cellHeightPx });
  }

  scroll(direction: "up" | "down", lines: number, source: "wheel" | "key" = "wheel"): void {
    this.sendControl({ type: "scroll", direction, lines, source });
  }

  private startPing() {
    this.stopPing();
    this.pingTimer = setInterval(() => this.sendControl({ type: "ping" }), 20_000);
  }
  private stopPing() {
    if (this.pingTimer) clearInterval(this.pingTimer);
    this.pingTimer = null;
  }

  close(): void {
    this.closedByClient = true;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.stopPing();
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.sendControl({ type: "release" });
    }
    this.ws?.close();
    this.ws = null;
  }
}
