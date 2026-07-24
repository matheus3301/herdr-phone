/**
 * Partitioned run state.
 *
 * High-frequency, per-run state (instruction delivery, observed transitions,
 * the composer draft) lives here rather than in the topology AppStore, so a run
 * that is updating every second never rerenders the inbox or the workspace
 * tree. Each run id is its own subscription channel; `useSyncExternalStore`
 * reads one partition at a time.
 *
 * Nothing here is persisted. Instruction text is user content bound for a shell,
 * so it never reaches localStorage, the service worker, or an export.
 */
import type { AgentStatus } from "./types";
import type { Run } from "./run";
import { uuid } from "./utils";

/* ------------------------------------------------------------- delivery */

/**
 * Delivery states for an instruction the operator sent.
 *
 * `delivery_unknown` is the honest outcome when the relay lost certainty after
 * Herdr may have accepted the prompt. There is no end-to-end idempotency key on
 * `agent.prompt` today, so the UI must never retry automatically — a duplicate
 * instruction to a live shell is worse than an unanswered one.
 */
export type DeliveryState = "pending" | "accepted" | "delivery_unknown" | "rejected";

export interface Instruction {
  /** Client message id. Reserved for an upstream idempotency key. */
  id: string;
  text: string;
  state: DeliveryState;
  error: string | null;
  createdAt: number;
  settledAt: number | null;
}

export const DELIVERY_LABEL: Record<DeliveryState, string> = {
  pending: "Sending",
  accepted: "Delivered",
  delivery_unknown: "Delivery unknown",
  rejected: "Not sent",
};

/* --------------------------------------------------------- observed feed */

/**
 * A transition this device actually witnessed. Herdr exposes no wall-clock
 * transition time — only a monotonic counter — so the timestamp is explicitly
 * local ("seen"), and the copy never implies the relay reported it.
 */
export interface ObservedEvent {
  id: string;
  kind: "status" | "instruction";
  text: string;
  status: AgentStatus | null;
  at: number;
  tone: "attention" | "active" | "settled" | "neutral";
}

const TONE_OF: Record<AgentStatus, ObservedEvent["tone"]> = {
  blocked: "attention",
  working: "active",
  done: "settled",
  idle: "neutral",
  unknown: "neutral",
};

const OBSERVED_TEXT: Record<AgentStatus, string> = {
  blocked: "Agent stopped for a decision",
  working: "Agent started working",
  done: "Background work settled",
  idle: "Agent went idle",
  unknown: "Herdr lost track of this agent",
};

export interface RunState {
  runId: string;
  instructions: Instruction[];
  observed: ObservedEvent[];
  draft: string;
  /** Last Herdr transition counter folded into `observed`. */
  lastSeq: number | null;
  lastStatus: AgentStatus | null;
}

const MAX_OBSERVED = 60;
const MAX_INSTRUCTIONS = 40;

const EMPTY: RunState = {
  runId: "",
  instructions: [],
  observed: [],
  draft: "",
  lastSeq: null,
  lastStatus: null,
};

function blank(runId: string): RunState {
  return { ...EMPTY, runId };
}

export class RunStore {
  private states = new Map<string, RunState>();
  private listeners = new Map<string, Set<() => void>>();
  private now: () => number;

  constructor(now: () => number = () => Date.now()) {
    this.now = now;
  }

  subscribe = (runId: string, cb: () => void): (() => void) => {
    let set = this.listeners.get(runId);
    if (!set) {
      set = new Set();
      this.listeners.set(runId, set);
    }
    set.add(cb);
    return () => {
      set!.delete(cb);
      if (set!.size === 0) this.listeners.delete(runId);
    };
  };

  get = (runId: string): RunState => {
    let state = this.states.get(runId);
    if (!state) {
      state = blank(runId);
      this.states.set(runId, state);
    }
    return state;
  };

  private patch(runId: string, next: Partial<RunState>) {
    const current = this.get(runId);
    this.states.set(runId, { ...current, ...next });
    const set = this.listeners.get(runId);
    if (set) for (const cb of set) cb();
  }

  /* ---- composer draft ---------------------------------------------- */

  setDraft(runId: string, draft: string): void {
    if (this.get(runId).draft === draft) return;
    this.patch(runId, { draft });
  }

  /* ---- instructions -------------------------------------------------- */

  /** Record an instruction as pending. Returns its client message id. */
  beginSend(runId: string, text: string): string {
    const id = uuid();
    const instruction: Instruction = {
      id,
      text,
      state: "pending",
      error: null,
      createdAt: this.now(),
      settledAt: null,
    };
    const instructions = [...this.get(runId).instructions, instruction].slice(-MAX_INSTRUCTIONS);
    this.patch(runId, { instructions });
    return id;
  }

  /** Settle an instruction. Only `accepted` and `delivery_unknown` clear a draft. */
  settleSend(runId: string, id: string, state: Exclude<DeliveryState, "pending">, error: string | null = null): void {
    const current = this.get(runId);
    const instructions = current.instructions.map((i) =>
      i.id === id ? { ...i, state, error, settledAt: this.now() } : i,
    );
    let observed = current.observed;
    if (state === "accepted") {
      observed = this.append(observed, {
        id: `sent-${id}`,
        kind: "instruction",
        text: "Instruction delivered to the agent",
        status: null,
        at: this.now(),
        tone: "neutral",
      });
    }
    this.patch(runId, { instructions, observed });
  }

  /** Drop a settled instruction the operator has acknowledged. */
  dismiss(runId: string, id: string): void {
    const instructions = this.get(runId).instructions.filter((i) => i.id !== id);
    this.patch(runId, { instructions });
  }

  /* ---- observed transitions ------------------------------------------ */

  private append(list: ObservedEvent[], event: ObservedEvent): ObservedEvent[] {
    return [...list, event].slice(-MAX_OBSERVED);
  }

  /**
   * Fold a snapshot projection into the observed feed. Only a change in Herdr's
   * transition counter produces an entry, so repeated identical snapshots (the
   * common case) are inert.
   */
  observe(run: Run): void {
    const current = this.get(run.id);
    if (current.lastSeq === run.stateChangeSeq && current.lastStatus === run.status) return;
    const firstSight = current.lastSeq === null;
    let observed = current.observed;
    if (!firstSight && current.lastStatus !== run.status) {
      observed = this.append(observed, {
        id: `${run.id}-${run.stateChangeSeq}-${run.status}`,
        kind: "status",
        text: OBSERVED_TEXT[run.status],
        status: run.status,
        at: this.now(),
        tone: TONE_OF[run.status],
      });
    }
    this.patch(run.id, { observed, lastSeq: run.stateChangeSeq, lastStatus: run.status });
  }

  /** Most recent local observation for a run, or null when nothing was seen. */
  lastSeenAt(runId: string): number | null {
    const observed = this.states.get(runId)?.observed;
    if (!observed || observed.length === 0) return null;
    return observed[observed.length - 1].at;
  }

  /** Forget a run's partition (its pane generation is gone). */
  forget(runId: string): void {
    this.states.delete(runId);
    const set = this.listeners.get(runId);
    if (set) for (const cb of set) cb();
  }
}

export const runStore = new RunStore();

/**
 * Fold the current run projection into the run store. Called from the inbox so
 * transitions are recorded whether or not the run detail is open — the feed is
 * "what this device saw", and it should not have gaps because a route was
 * closed.
 */
export function observeRuns(runs: Run[], store: RunStore = runStore): void {
  for (const run of runs) store.observe(run);
}
