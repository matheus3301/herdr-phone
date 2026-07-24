/**
 * Jittered exponential backoff for WebSocket reconnection (SPEC §16).
 * Deterministic given an injected RNG so it is unit-testable.
 */

export interface BackoffOptions {
  baseMs?: number;
  maxMs?: number;
  factor?: number;
  /** 0..1 jitter fraction applied symmetrically. */
  jitter?: number;
  rng?: () => number;
}

export class Backoff {
  private attempt = 0;
  private readonly baseMs: number;
  private readonly maxMs: number;
  private readonly factor: number;
  private readonly jitter: number;
  private readonly rng: () => number;

  constructor(opts: BackoffOptions = {}) {
    this.baseMs = opts.baseMs ?? 500;
    this.maxMs = opts.maxMs ?? 15_000;
    this.factor = opts.factor ?? 2;
    this.jitter = opts.jitter ?? 0.25;
    this.rng = opts.rng ?? Math.random;
  }

  /** Next delay in ms, advancing the attempt counter. */
  next(): number {
    const raw = Math.min(this.maxMs, this.baseMs * this.factor ** this.attempt);
    this.attempt += 1;
    const spread = raw * this.jitter;
    const delta = (this.rng() * 2 - 1) * spread;
    return Math.max(0, Math.round(raw + delta));
  }

  /** Reset after a successful, stable connection. */
  reset(): void {
    this.attempt = 0;
  }

  get attempts(): number {
    return this.attempt;
  }
}

/**
 * The DOM events that should trigger an immediate revalidation/reconnect
 * (SPEC §16). `navigator.onLine` is deliberately excluded as a gate.
 */
export const REVALIDATE_EVENTS = [
  "visibilitychange",
  "pageshow",
  "focus",
  "online",
  "freeze",
  "resume",
] as const;

/**
 * Subscribe to all revalidation triggers. Returns an unsubscribe function.
 * `visibilitychange` only fires the callback when the page becomes visible.
 */
export function onRevalidate(cb: () => void): () => void {
  const handlers: Array<[string, EventTarget, EventListener]> = [];
  const add = (target: EventTarget, name: string, listener: EventListener) => {
    target.addEventListener(name, listener);
    handlers.push([name, target, listener]);
  };

  const wake: EventListener = () => cb();
  const visibility: EventListener = () => {
    if (document.visibilityState === "visible") cb();
  };

  add(window, "pageshow", wake);
  add(window, "focus", wake);
  add(window, "online", wake);
  add(document, "visibilitychange", visibility);
  // freeze/resume are on document in the Page Lifecycle API. `resume` fires when
  // a frozen page is restored; `freeze` is registered per SPEC §16's trigger set
  // (it also lets the app flush/re-poll right before the tab is frozen).
  add(document, "freeze", wake);
  add(document, "resume", wake);

  return () => {
    for (const [name, target, listener] of handlers) target.removeEventListener(name, listener);
  };
}
