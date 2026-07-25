/**
 * The run adapter boundary.
 *
 * `detectRunFidelity` reads the capability document and `createRunAdapter`
 * returns the implementation that matches. Two implementations exist, and which
 * one is live is decided by capability advertisement alone — never by probing a
 * route or sniffing a payload:
 *
 *  - **observed** — the relay advertises the versioned structured run contract
 *    (SPEC §12.1). Runs come from `GET /api/v1/runs`, and one run's bounded
 *    output comes from `GET /api/v1/runs/{pane_id}` guarded by the mandatory
 *    `expected_generation`. This build's contract emits exactly one part type,
 *    `observed_terminal_output`, which is terminal output Herdr rendered. It
 *    carries no role, so it is presented as terminal output and nothing else.
 *  - **terminal-output** — the fallback for an older relay: the topology
 *    snapshot plus a bounded `pane.read`, labelled as recent terminal output.
 *
 * `structured` is reserved for a relay that advertises `structured_messages`.
 * No build does today, and until one does the UI must not render a message, a
 * tool call, an approval, a diff, or a test result. Unknown part types are
 * ignored rather than guessed at.
 */
import * as api from "./api";
import { ApiError } from "./api";
import { RUN_CONTRACT_VERSION } from "./normalize";
import type { Capabilities, MutationResponse, ReadSource, RunContract, WireRunSummary } from "./types";
import type { RunKey } from "./run";

export type RunFidelity = "structured" | "observed" | "terminal-output";

/** Outcome of an instruction send. Never "maybe retry" — the caller decides. */
export type SendOutcome =
  | { kind: "accepted" }
  | { kind: "rejected"; code: string; message: string }
  | { kind: "delivery_unknown"; message: string };

/** The part type the structured contract emits for rendered terminal output. */
export const PART_OBSERVED_TERMINAL_OUTPUT = "observed_terminal_output";

/**
 * Bounded terminal output for one run, from either source. `origin` records
 * which, because the copy differs — but both are terminal output and neither is
 * ever presented as an agent message.
 */
export interface ObservedOutput {
  origin: "run-contract" | "pane-read";
  paneId: string;
  source: string;
  /** Line bound the relay applied (the value it echoed back). */
  lines: number;
  bytes: number;
  /** True when the relay dropped older output to fit its byte bound. */
  truncated: boolean;
  text: string;
  readAt: number;
  /** Contract part types present in the response that this build cannot render. */
  ignoredPartTypes: string[];
}

/**
 * Stable relay error codes a run read can produce (SPEC §12.1), plus the two
 * client-side transport outcomes. Every one maps to a static message: a relay
 * message can quote pane content, so none is ever displayed.
 */
export type RunErrorCode =
  | "generation_stale"
  | "run_unavailable"
  | "unavailable"
  | "deadline_exceeded"
  | "run_read_failed"
  | "unsupported"
  | "bad_request"
  | "network";

export const RUN_ERROR_MESSAGE: Record<RunErrorCode, string> = {
  generation_stale: "This pane changed since you opened the run, so nothing was read.",
  run_unavailable: "No agent run occupies this pane any more.",
  unavailable: "The relay could not reach Herdr. Nothing was read.",
  deadline_exceeded: "Herdr did not answer in time. Nothing was read.",
  run_read_failed: "Herdr could not read this pane.",
  unsupported: "This Herdr build cannot read output for this pane.",
  bad_request: "The relay refused the read request.",
  network: "Could not reach the relay.",
};

const RUN_ERROR_CODES = new Set<string>(Object.keys(RUN_ERROR_MESSAGE));

function runErrorCode(code: string): RunErrorCode {
  return RUN_ERROR_CODES.has(code) ? (code as RunErrorCode) : "run_read_failed";
}

export type RunOutputResult =
  | { kind: "ok"; output: ObservedOutput; run: WireRunSummary | null }
  | {
      kind: "error";
      code: RunErrorCode;
      message: string;
      /** True when the failure means the open run's identity is no longer valid. */
      invalidates: boolean;
    };

export interface RunAdapter {
  fidelity: RunFidelity;
  /** True when the relay can render structured messages for a run. */
  supportsMessages: boolean;
  /** True when the run list comes from the relay's structured contract. */
  usesRunContract: boolean;
  /** True when bounded terminal output can be read at all. */
  supportsObservedOutput: boolean;
  /** True when validated logical keys can be delivered without a console. */
  supportsKeys: boolean;
  /** The advertised contract, or null in fallback mode. */
  contract: RunContract | null;
  /** Line bound to request: the UI default, clamped to what the relay allows. */
  outputLines: number;
  /** Bounded recent terminal output for one run. */
  readRunOutput: (run: RunKey, signal?: AbortSignal) => Promise<RunOutputResult>;
}

export function detectRunFidelity(capabilities: Capabilities | null): RunFidelity {
  const runs = capabilities?.runs;
  if (!runs?.supported) return "terminal-output";
  // Defence in depth: the wire normalizer already rejects a contract version
  // this build does not implement, and so does this gate.
  if (runs.contractVersion !== RUN_CONTRACT_VERSION) return "terminal-output";
  return runs.structuredMessages ? "structured" : "observed";
}

/** How many trailing lines of pane output the run view asks for. */
export const RECENT_OUTPUT_LINES = 40;

/** The contract source the run view asks for; unwrapped reads best on a phone. */
const RUN_OUTPUT_SOURCE: ReadSource = "recent-unwrapped";

/** The fallback source. `pane.read` has no unwrapped bound worth the extra risk. */
const FALLBACK_OUTPUT_SOURCE: ReadSource = "recent";

export function createRunAdapter(capabilities: Capabilities | null): RunAdapter {
  const fidelity = detectRunFidelity(capabilities);
  const contract = capabilities?.runs ?? null;
  const ops = new Set(capabilities?.operations ?? []);
  const usesRunContract = fidelity !== "terminal-output";
  const maxLines = contract?.maxOutputLines ?? 0;
  const outputLines = maxLines > 0 ? Math.min(RECENT_OUTPUT_LINES, maxLines) : RECENT_OUTPUT_LINES;
  const source = usesRunContract && contract?.outputSources.includes(RUN_OUTPUT_SOURCE)
    ? RUN_OUTPUT_SOURCE
    : FALLBACK_OUTPUT_SOURCE;

  return {
    fidelity,
    supportsMessages: fidelity === "structured",
    usesRunContract,
    supportsObservedOutput: usesRunContract ? !!contract?.observedTerminalOutput : true,
    supportsKeys: ops.has("agent.send_keys"),
    contract,
    outputLines,
    readRunOutput: (run, signal) => {
      // Live generations start at 1. Without one the relay would refuse the
      // read, so it is refused here rather than spending a round trip.
      if (!Number.isInteger(run.generation) || run.generation <= 0) {
        return Promise.resolve<RunOutputResult>({
          kind: "error",
          code: "generation_stale",
          message: RUN_ERROR_MESSAGE.generation_stale,
          invalidates: false,
        });
      }
      return usesRunContract
        ? readFromContract(run, source, outputLines, contract, signal)
        : readFromPane(run, outputLines, signal);
    },
  };
}

/**
 * Read one run through the structured contract. The generation the caller holds
 * is asserted, so a recycled pane fails closed with `generation_stale` instead
 * of returning another occupant's output.
 */
async function readFromContract(
  run: RunKey,
  source: ReadSource,
  lines: number,
  contract: RunContract | null,
  signal?: AbortSignal,
): Promise<RunOutputResult> {
  if (contract && !contract.observedTerminalOutput) {
    return { kind: "error", code: "unsupported", message: RUN_ERROR_MESSAGE.unsupported, invalidates: false };
  }
  try {
    const res = await api.getRun(run.paneId, {
      expectedGeneration: run.generation,
      source,
      lines,
      signal,
    });
    const parts = res.parts ?? [];
    // Exactly the one part type this build understands is rendered. Anything
    // else is counted and ignored: a client must never interpret a part as a
    // message unless its type says so.
    const observed = parts.find((p) => p.type === PART_OBSERVED_TERMINAL_OUTPUT) ?? null;
    const ignoredPartTypes = [
      ...new Set(parts.filter((p) => p.type !== PART_OBSERVED_TERMINAL_OUTPUT).map((p) => p.type)),
    ];
    return {
      kind: "ok",
      run: res.run ?? null,
      output: {
        origin: "run-contract",
        paneId: res.run?.pane_id || run.paneId,
        source: observed?.source ?? source,
        lines: observed ? observed.lines : 0,
        bytes: observed?.bytes ?? 0,
        truncated: !!observed?.truncated,
        text: observed?.text ?? "",
        readAt: Date.now(),
        ignoredPartTypes,
      },
    };
  } catch (err) {
    return failure(err);
  }
}

/** Read the pane directly — the fallback for a relay without the contract. */
async function readFromPane(run: RunKey, lines: number, signal?: AbortSignal): Promise<RunOutputResult> {
  try {
    const res = await api.readPane(run.paneId, FALLBACK_OUTPUT_SOURCE, lines, signal);
    return {
      kind: "ok",
      run: null,
      output: {
        origin: "pane-read",
        paneId: res.pane_id || run.paneId,
        source: res.source || FALLBACK_OUTPUT_SOURCE,
        // The legacy route echoes the requested bound, not the amount returned.
        // Count the text so the UI never presents that request as observed fact.
        lines: countLines(res.content),
        bytes: res.content.length,
        // `pane.read` has no truncation signal; it returns the tail it was asked for.
        truncated: false,
        text: res.content,
        readAt: Date.now(),
        ignoredPartTypes: [],
      },
    };
  } catch (err) {
    return failure(err);
  }
}

function countLines(text: string): number {
  if (!text) return 0;
  const breaks = text.match(/\n/g)?.length ?? 0;
  return text.endsWith("\n") ? breaks : breaks + 1;
}

function failure(err: unknown): RunOutputResult {
  if (err instanceof ApiError) {
    const code = runErrorCode(err.code === "timeout" ? "deadline_exceeded" : err.code);
    return {
      kind: "error",
      code,
      message: RUN_ERROR_MESSAGE[code],
      // A stale generation is the one failure that means the open run itself is
      // no longer valid: the pane it names now belongs to someone else.
      invalidates: code === "generation_stale",
    };
  }
  return { kind: "error", code: "network", message: RUN_ERROR_MESSAGE.network, invalidates: false };
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
