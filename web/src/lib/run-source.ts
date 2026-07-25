/**
 * The run list, from whichever authority the relay advertises.
 *
 * This is the single place that decides where runs come from:
 *
 *  - When the relay advertises the versioned structured run contract, the list
 *    is `GET /api/v1/runs`. Its `run_id`s are authoritative and opaque, and its
 *    `truncated` flag is honoured so a bounded list is never read as complete.
 *  - Otherwise the list is projected from the topology snapshot, with internal
 *    pane+generation ids that are never sent to the relay.
 *
 * Snapshot remains truth and events remain wakeups: no new poll and no new event
 * type is introduced. A snapshot change (however it arrived) is the signal to
 * refetch the run list, exactly as SPEC §12.1 describes, and a missed wakeup
 * costs one poll interval rather than correctness.
 *
 * It is a separate external store from the AppStore so a run list refresh does
 * not rerender topology consumers, and nothing here is persisted.
 */
import * as api from "./api";
import { ApiError } from "./api";
import { store as appStore, type AppStore, type AppState } from "./store";
import { buildRuns, runsFromWire, type Run } from "./run";
import { detectRunFidelity, RUN_ERROR_MESSAGE, type RunErrorCode, type RunFidelity } from "./run-adapter";
import { observeRuns } from "./run-store";
import type { Snapshot } from "./types";

export interface RunListState {
  runs: Run[];
  /** Which authority produced `runs`. */
  fidelity: RunFidelity;
  /** True while the first structured list for the current relay is in flight. */
  loading: boolean;
  /** True when the relay's `max_runs` bound cut the list short. */
  truncated: boolean;
  /** The bound that applied, for the truncation notice. 0 when unknown. */
  maxRuns: number;
  /** Static message for a failed list read. Never a relay-supplied string. */
  error: string | null;
  /** Content hash of the snapshot the list was last reconciled against. */
  snapshotHash: string | null;
}

const EMPTY: RunListState = {
  runs: [],
  fidelity: "terminal-output",
  loading: false,
  truncated: false,
  maxRuns: 0,
  error: null,
  snapshotHash: null,
};

const LIST_ERROR_CODES = new Set<string>(Object.keys(RUN_ERROR_MESSAGE));

function listError(err: unknown): string {
  if (err instanceof ApiError && LIST_ERROR_CODES.has(err.code)) {
    return RUN_ERROR_MESSAGE[err.code as RunErrorCode];
  }
  return "The relay could not list runs.";
}

export class RunSource {
  private state: RunListState = EMPTY;
  private listeners = new Set<() => void>();
  private detach: (() => void) | null = null;
  private app: AppStore;

  /** The snapshot hash the in-flight or last completed fetch was made for. */
  private fetchedHash: string | null = null;
  /** The snapshot the fallback projection was last built from. */
  private lastSnapshot: Snapshot | null = null;
  private inFlight = false;
  private pending = false;

  constructor(app: AppStore = appStore) {
    this.app = app;
  }

  subscribe = (cb: () => void): (() => void) => {
    this.listeners.add(cb);
    if (this.listeners.size === 1) this.attach();
    return () => {
      this.listeners.delete(cb);
      if (this.listeners.size === 0) this.release();
    };
  };

  /**
   * The current list. A read before anything has subscribed reconciles first,
   * so the very first render sees the projected list rather than an empty one.
   * There are no listeners at that point, so nothing is notified mid-render.
   */
  getState = (): RunListState => {
    if (!this.detach) this.reconcile();
    return this.state;
  };

  private attach() {
    if (this.detach) return;
    this.detach = this.app.subscribe(() => this.reconcile());
    this.reconcile();
  }

  private release() {
    this.detach?.();
    this.detach = null;
  }

  private set(patch: Partial<RunListState>) {
    this.state = { ...this.state, ...patch };
    for (const cb of this.listeners) cb();
  }

  /** Recompute from the current app state, fetching the structured list if due. */
  private reconcile(app: AppState = this.app.getState()) {
    const fidelity = detectRunFidelity(app.capabilities);
    if (fidelity === "terminal-output") {
      this.fetchedHash = null;
      // Only the snapshot can change a projected list. Connection ticks and
      // session changes must not churn it.
      if (app.snapshot === this.lastSnapshot && this.state.fidelity === fidelity) return;
      this.lastSnapshot = app.snapshot;
      const runs = buildRuns(app.snapshot);
      observeRuns(runs);
      this.set({
        runs,
        fidelity,
        loading: false,
        truncated: false,
        maxRuns: 0,
        error: null,
        snapshotHash: app.snapshot?.hash ?? null,
      });
      return;
    }

    const hash = app.snapshot?.hash ?? null;
    this.lastSnapshot = app.snapshot;
    if (this.state.fidelity !== fidelity) {
      // The relay just told us it speaks the contract: drop the projected list
      // rather than leaving internal ids on screen alongside authoritative ones.
      this.set({ runs: [], fidelity, truncated: false, error: null, loading: true });
      this.fetchedHash = null;
    }
    if (this.fetchedHash === hash) return;
    void this.fetch(hash);
  }

  /** Fetch the structured list, coalescing overlapping snapshot wakeups. */
  private async fetch(hash: string | null): Promise<void> {
    if (this.inFlight) {
      this.pending = true;
      return;
    }
    this.inFlight = true;
    this.fetchedHash = hash;
    if (this.state.runs.length === 0) this.set({ loading: true });
    try {
      const res = await api.getRuns();
      const snapshot = this.app.getState().snapshot;
      const runs = runsFromWire(res.runs ?? [], snapshot);
      observeRuns(runs);
      this.set({
        runs,
        loading: false,
        truncated: !!res.truncated,
        maxRuns: res.capabilities?.max_runs ?? 0,
        error: null,
        snapshotHash: res.snapshot_hash || hash,
      });
    } catch (err) {
      // Keep the last good list: a failed refresh must not empty the inbox, and
      // it must not silently fall back to internal ids either. The next snapshot
      // wakeup (or an explicit refresh) retries; nothing hammers the relay.
      this.set({ loading: false, error: listError(err) });
    } finally {
      this.inFlight = false;
      if (this.pending) {
        this.pending = false;
        this.reconcile();
      }
    }
  }

  /** Force a refresh — used by an explicit retry after a failed list read. */
  refresh = (): void => {
    this.fetchedHash = null;
    this.lastSnapshot = null;
    this.reconcile();
  };

  /**
   * Test seam: forget the list so run mode is decided fresh from the current
   * capabilities. Anything still subscribed is re-derived, not orphaned.
   */
  reset(): void {
    this.release();
    this.state = EMPTY;
    this.fetchedHash = null;
    this.lastSnapshot = null;
    this.inFlight = false;
    this.pending = false;
    if (this.listeners.size > 0) this.attach();
  }
}

export const runSource = new RunSource();
