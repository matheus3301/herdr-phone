/**
 * The run adapter boundary.
 *
 * A real agent-first experience needs a versioned structured source of runs and
 * messages from Herdr or a trusted agent adapter. The relay does not expose one
 * yet, so this module is the seam: `detectRunFidelity` reads the capability
 * document, and `createRunAdapter` returns the implementation that matches.
 *
 * Today the only implementation is the honest fallback. It sends instructions
 * through the existing typed `agent.prompt` mutation and reads a bounded slice
 * of `pane.read` that the UI labels as recent terminal output — never as an
 * assistant message, a tool call, an approval, a diff, or a test result. When a
 * structured contract lands it registers here and the UI upgrades without any
 * component learning about the transport.
 */
import * as api from "./api";
import { ApiError } from "./api";
import type { Capabilities, MutationResponse } from "./types";

export type RunFidelity = "structured" | "terminal-output";

/** Outcome of an instruction send. Never "maybe retry" — the caller decides. */
export type SendOutcome =
  | { kind: "accepted" }
  | { kind: "rejected"; code: string; message: string }
  | { kind: "delivery_unknown"; message: string };

export interface TerminalTail {
  paneId: string;
  lines: number;
  content: string;
  readAt: number;
}

export interface RunAdapter {
  fidelity: RunFidelity;
  /** True when the relay can render structured messages for a run. */
  supportsMessages: boolean;
  /** True when validated logical keys can be delivered without a console. */
  supportsKeys: boolean;
  /** Bounded recent terminal output, or null when the relay cannot provide it. */
  readRecentOutput: (paneId: string, lines: number, signal?: AbortSignal) => Promise<TerminalTail | null>;
}

/**
 * A structured run contract must announce itself explicitly. Until the relay
 * advertises both the read and the send half of it, the UI fails closed to
 * terminal-output mode rather than guessing from the shape of a payload.
 */
const STRUCTURED_OPERATIONS = ["run.send", "run.respond"];

export function detectRunFidelity(capabilities: Capabilities | null): RunFidelity {
  if (!capabilities) return "terminal-output";
  const ops = new Set(capabilities.operations);
  return STRUCTURED_OPERATIONS.every((op) => ops.has(op)) ? "structured" : "terminal-output";
}

/** How many trailing lines of pane output the run view asks for. */
export const RECENT_OUTPUT_LINES = 40;

export function createRunAdapter(capabilities: Capabilities | null): RunAdapter {
  const fidelity = detectRunFidelity(capabilities);
  const ops = new Set(capabilities?.operations ?? []);
  return {
    fidelity,
    supportsMessages: fidelity === "structured",
    supportsKeys: ops.has("agent.send_keys"),
    readRecentOutput: async (paneId, lines, signal) => {
      try {
        const res = await api.readPane(paneId, "recent", lines, signal);
        return { paneId: res.pane_id || paneId, lines: res.lines, content: res.content, readAt: Date.now() };
      } catch {
        // A pane that no longer exists, a relay hiccup, or a read the operator
        // is not allowed: the run view degrades to "no output available".
        return null;
      }
    },
  };
}

/**
 * Classify a mutation result into a delivery outcome.
 *
 * A retryable failure means the relay could not establish whether Herdr acted.
 * The prompt may already be in the agent's input, so this is reported as
 * `delivery_unknown` and never retried on the user's behalf. Deterministic
 * refusals (stale generation, bad params, no session) are plain rejections and
 * the composer keeps the text.
 */
export function classifySend(res: MutationResponse | null, thrown?: unknown): SendOutcome {
  if (thrown instanceof ApiError) {
    return thrown.retryable
      ? { kind: "delivery_unknown", message: thrown.message }
      : { kind: "rejected", code: thrown.code, message: thrown.message };
  }
  if (thrown) {
    return { kind: "delivery_unknown", message: "The relay did not answer. The instruction may have been delivered." };
  }
  if (!res) {
    return { kind: "delivery_unknown", message: "The relay did not answer. The instruction may have been delivered." };
  }
  if ("error" in res && res.error) {
    return res.error.retryable
      ? { kind: "delivery_unknown", message: res.error.message }
      : { kind: "rejected", code: res.error.code, message: res.error.message };
  }
  return { kind: "accepted" };
}
