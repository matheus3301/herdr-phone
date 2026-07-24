/**
 * App store (SPEC §16). Single external store read via useSyncExternalStore. Owns
 * the paired session, capabilities, the live topology snapshot (normalized from
 * the backend wire shape), and connection health. Snapshots arrive over /events
 * as `{type:"snapshot", snapshot}`; ETag polling of /snapshot is the fallback.
 * Health is derived from one clock. navigator.onLine is never a gate.
 */
import * as api from "./api";
import { Backoff, onRevalidate } from "./reconnect";
import { normalizeCapabilities, normalizeSnapshot, sessionFromResponse } from "./normalize";
import type {
  Capabilities,
  ConfirmationRequest,
  ConfirmationResult,
  EventsServerMessage,
  MutationOperation,
  MutationRequest,
  MutationResponse,
  SessionInfo,
  Snapshot,
  WirePairResponse,
  WireSnapshotEnvelope,
} from "./types";
import { uuid } from "./utils";

export type ConnectionState = "connecting" | "live" | "trouble" | "lost";

export interface AppState {
  ready: boolean;
  session: SessionInfo | null;
  capabilities: Capabilities | null;
  snapshot: Snapshot | null;
  connection: ConnectionState;
  lastError: string | null;
  /** True when paired but without a CSRF token — mutations blocked. Normally
   * false: both POST /pair and GET /session return the CSRF token. */
  readOnly: boolean;
}

const TROUBLE_MS = 4000;
const LOST_MS = 12000;
const HOT_POLL_MS = 1500;
const COLD_POLL_MS = 12000;
const PHONE_VERSION = typeof __APP_VERSION__ !== "undefined" ? __APP_VERSION__ : "0.0.0";

function wsUrl(path: string): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}${api.API_BASE}${path}`;
}

export class AppStore {
  private state: AppState = {
    ready: false,
    session: null,
    capabilities: null,
    snapshot: null,
    connection: "connecting",
    lastError: null,
    readOnly: false,
  };

  /**
   * Wall-clock of the last confirmed liveness. Kept OUT of AppState (internal
   * bookkeeping only) so refreshing it every health tick while the socket is
   * open never triggers a component re-render. It marks when the socket last
   * stopped being OPEN, so the trouble/lost progression measures elapsed time
   * since a disconnect, not since the last application message.
   */
  private lastLiveMs = 0;

  private listeners = new Set<() => void>();
  private ws: WebSocket | null = null;
  private backoff = new Backoff({ baseMs: 500, maxMs: 15_000 });
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private healthTimer: ReturnType<typeof setInterval> | null = null;
  private pollTimer: ReturnType<typeof setTimeout> | null = null;
  private etag: string | null = null;
  private started = false;
  private disposeRevalidate: (() => void) | null = null;
  private now: () => number;

  constructor(now: () => number = () => Date.now()) {
    this.now = now;
  }

  subscribe = (cb: () => void): (() => void) => {
    this.listeners.add(cb);
    if (this.listeners.size === 1) this.startHealthClock();
    return () => {
      this.listeners.delete(cb);
      if (this.listeners.size === 0) this.stopHealthClock();
    };
  };

  getState = (): AppState => this.state;

  private set(patch: Partial<AppState>) {
    this.state = { ...this.state, ...patch };
    for (const cb of this.listeners) cb();
  }

  private everLive = false;

  private markLive() {
    this.everLive = true;
    this.lastLiveMs = this.now();
    if (this.state.connection !== "live" || this.state.lastError) {
      this.set({ connection: "live", lastError: null });
    }
  }

  /** Establish the session from a fresh pairing (carries the CSRF token). */
  setSessionFromPair(pair: WirePairResponse) {
    const session = sessionFromResponse(pair);
    this.set({ session, readOnly: !session.csrfToken });
  }

  async start(): Promise<void> {
    if (this.started) return;
    this.started = true;
    // Start the liveness grace window now, so the health clock reports
    // "connecting" (no alarm) while the first /events socket is establishing,
    // rather than instantly "lost" from a zero lastLiveMs.
    this.lastLiveMs = this.now();
    try {
      const [sessionResp, caps] = await Promise.all([api.getSession(), api.getCapabilities()]);
      // GET /session now carries the CSRF token + expiry, so a cold page/PWA
      // reload recovers a fully mutable session without re-pairing. Keep an
      // existing paired session if we already have one this page load.
      const existing = this.state.session;
      const session = existing?.csrfToken ? existing : sessionFromResponse(sessionResp);
      this.set({
        session,
        capabilities: normalizeCapabilities(caps, PHONE_VERSION),
        readOnly: !session.csrfToken,
        ready: true,
      });
    } catch (err) {
      this.set({ ready: true, lastError: describeError(err) });
    }
    this.openSocket();
    this.disposeRevalidate = onRevalidate(() => this.revalidate());
  }

  stop(): void {
    this.started = false;
    this.ws?.close();
    this.ws = null;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    if (this.pollTimer) clearTimeout(this.pollTimer);
    this.disposeRevalidate?.();
    this.disposeRevalidate = null;
  }

  private openSocket() {
    if (!this.started) return;
    try {
      this.ws = new WebSocket(wsUrl("/events"));
    } catch {
      this.scheduleReconnect();
      return;
    }
    const ws = this.ws;
    ws.onopen = () => {
      this.backoff.reset();
      this.markLive();
    };
    ws.onmessage = (ev) => {
      this.markLive();
      try {
        const msg = JSON.parse(ev.data as string) as EventsServerMessage;
        if (msg.type === "snapshot") this.ingest(msg.snapshot);
      } catch {
        /* ignore malformed frames; poll fallback keeps state correct */
      }
    };
    ws.onclose = () => {
      if (this.ws === ws) {
        this.ws = null;
        this.scheduleReconnect();
        this.startPollFallback();
      }
    };
    ws.onerror = () => ws.close();
  }

  private ingest(env: WireSnapshotEnvelope) {
    if (!this.state.snapshot || env.hash !== this.state.snapshot.hash) {
      const view = normalizeSnapshot(env);
      if (view) this.set({ snapshot: view });
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    const delay = this.backoff.next();
    this.reconnectTimer = setTimeout(() => this.openSocket(), delay);
  }

  revalidate = (): void => {
    if (!this.started) return;
    if (!this.ws || this.ws.readyState > WebSocket.OPEN) {
      this.backoff.reset();
      if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
      this.openSocket();
    }
    void this.pollOnce();
  };

  private startPollFallback() {
    if (this.pollTimer) return;
    const tick = async () => {
      await this.pollOnce();
      if (!this.started) return;
      const hot = this.hasActivity();
      this.pollTimer = setTimeout(tick, hot ? HOT_POLL_MS : COLD_POLL_MS);
    };
    this.pollTimer = setTimeout(tick, 0);
  }

  private stopPollFallback() {
    if (this.pollTimer) clearTimeout(this.pollTimer);
    this.pollTimer = null;
  }

  private async pollOnce() {
    try {
      const { snapshot, etag, notModified } = await api.getSnapshot(this.etag);
      this.markLive();
      if (etag) this.etag = etag;
      if (!notModified && snapshot) this.ingest(snapshot);
      if (this.ws && this.ws.readyState === WebSocket.OPEN) this.stopPollFallback();
    } catch (err) {
      this.set({ lastError: describeError(err) });
    }
  }

  private hasActivity(): boolean {
    const s = this.state.snapshot;
    if (!s) return true;
    return s.agents.some((a) => a.status === "working" || a.status === "blocked");
  }

  private startHealthClock() {
    if (this.healthTimer) return;
    this.healthTimer = setInterval(() => this.recomputeHealth(), 1000);
  }
  private stopHealthClock() {
    if (this.healthTimer) clearInterval(this.healthTimer);
    this.healthTimer = null;
  }
  private recomputeHealth() {
    // An OPEN socket is authoritatively live regardless of how long since the
    // last APPLICATION message. The server keeps the socket alive with WebSocket
    // control-frame Ping/Pong (~25s) and closes dead peers — that liveness is
    // invisible to browser JS, so keying health on application-message age falsely
    // marked a healthy idle socket trouble at 4s and lost at 12s. readyState is
    // the real signal. While open, refresh lastLiveMs so the trouble/lost timer
    // measures elapsed time from a *disconnect*, not from the last message.
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.everLive = true;
      this.lastLiveMs = this.now();
      if (this.state.connection !== "live") this.set({ connection: "live" });
      return;
    }

    // Not open (connecting / reconnecting / closed): escalate by elapsed time.
    const since = this.now() - this.lastLiveMs;
    let next: ConnectionState;
    if (!this.everLive) {
      // First connection still establishing: no alarm until the grace elapses.
      next = since >= LOST_MS ? "lost" : "connecting";
    } else if (since >= LOST_MS) {
      next = "lost";
    } else if (since >= TROUBLE_MS) {
      next = "trouble";
    } else {
      // Brief grace after a drop so a fast reconnect doesn't flash "reconnecting".
      next = "live";
    }
    if (next !== this.state.connection) this.set({ connection: next });
  }

  private csrf(): string {
    return this.state.session?.csrfToken ?? "";
  }

  canMutate(): boolean {
    return !!this.state.session?.csrfToken;
  }

  async runMutation(
    operation: MutationOperation,
    params: Record<string, unknown>,
    opts: { expectedGeneration?: number; confirmation?: string } = {},
  ): Promise<MutationResponse> {
    if (!this.csrf()) {
      // Defensive: both /pair and /session issue a CSRF token, so this is only
      // reached if no session was established. Fail with a clear, non-retryable
      // signal rather than a doomed round trip.
      return {
        request_id: "",
        error: { code: "reauth_required", message: "Session needs re-pairing before you can make changes.", retryable: false },
      };
    }
    const now = this.now();
    const req: MutationRequest = {
      request_id: uuid(),
      operation,
      deadline_unix_ms: now + 15_000,
      params,
    };
    if (opts.expectedGeneration !== undefined) req.expected_generation = opts.expectedGeneration;
    if (opts.confirmation) req.confirmation = opts.confirmation;
    const res = await api.mutate(req, this.csrf());
    void this.pollOnce();
    return res;
  }

  async prepareConfirmation(body: ConfirmationRequest): Promise<ConfirmationResult> {
    return api.prepareConfirmation(body, this.csrf());
  }
}

function describeError(err: unknown): string {
  if (err instanceof api.ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "Unexpected error";
}

export const store = new AppStore();
