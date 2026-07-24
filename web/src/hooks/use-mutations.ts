import { useCallback, useState } from "react";
import { store } from "@/lib/store";
import { ApiError } from "@/lib/api";
import { requiresConfirmation } from "@/lib/confirm";
import { checkPaneTarget, isPaneScoped, paneParams, type PaneTarget } from "@/lib/pane-ops";
import type { MutationOperation, MutationResponse } from "@/lib/types";

export interface RunOptions {
  expectedGeneration?: number;
  /** For destructive ops: the resource id the confirmation nonce is bound to. */
  resourceId?: string;
  confirmation?: string;
}

export interface MutationState {
  pending: boolean;
  error: string | null;
}

function refusal(code: string, message: string): MutationResponse {
  return { request_id: "", error: { code, message, retryable: false } };
}

/**
 * Dispatch typed, allowlisted mutations.
 *
 * `runPane` is the only supported path for a pane- or agent-scoped operation:
 * it forces the canonical `pane_id`, attaches the current lifecycle generation,
 * strips any dispatcher-preferred alias, and refuses locally — with a message
 * the operator can act on — when the generation is missing. Destructive
 * operations still carry a single-use server confirmation nonce obtained
 * through `prepareConfirmation`.
 */
export function useMutations() {
  const [state, setState] = useState<MutationState>({ pending: false, error: null });

  const run = useCallback(
    async (op: MutationOperation, params: Record<string, unknown>, opts: RunOptions = {}): Promise<MutationResponse | null> => {
      if (requiresConfirmation(op) && !opts.confirmation) {
        setState({ pending: false, error: "This action needs confirmation." });
        return refusal("confirmation_required", "This action needs confirmation.");
      }
      // Defence in depth: a pane-scoped operation must never leave the client
      // without a nonzero generation, whatever call site assembled it.
      if (isPaneScoped(op)) {
        const problem = checkPaneTarget({
          paneId: String(params.pane_id ?? ""),
          generation: opts.expectedGeneration ?? 0,
        });
        if (problem) {
          setState({ pending: false, error: problem.message });
          return refusal(problem.code, problem.message);
        }
      }
      setState({ pending: true, error: null });
      try {
        const res = await store.runMutation(op, params, {
          expectedGeneration: opts.expectedGeneration,
          confirmation: opts.confirmation,
        });
        if ("error" in res && res.error) {
          setState({ pending: false, error: res.error.message });
          return res;
        }
        setState({ pending: false, error: null });
        return res;
      } catch (err) {
        // A non-2xx response arrives as a thrown ApiError. Collapsing it to
        // `null` would erase the relay's code and its retryable flag, and the
        // caller needs both: "rejected" and "delivery unknown" are different
        // outcomes with different recoveries.
        const response: MutationResponse =
          err instanceof ApiError
            ? { request_id: "", error: { code: err.code, message: err.message, retryable: err.retryable } }
            : {
                request_id: "",
                error: { code: "network", message: "The relay did not answer.", retryable: true },
              };
        setState({ pending: false, error: response.error!.message });
        return response;
      }
    },
    [],
  );

  /** Run a pane- or agent-scoped mutation against a validated pane target. */
  const runPane = useCallback(
    async (
      op: MutationOperation,
      target: PaneTarget | null | undefined,
      extra: Record<string, unknown> = {},
      opts: Omit<RunOptions, "expectedGeneration"> = {},
    ): Promise<MutationResponse | null> => {
      const problem = checkPaneTarget(target);
      if (problem) {
        setState({ pending: false, error: problem.message });
        return refusal(problem.code, problem.message);
      }
      return run(op, paneParams(target!, extra), { ...opts, expectedGeneration: target!.generation });
    },
    [run],
  );

  const prepareConfirmation = useCallback(
    async (op: MutationOperation, resourceId: string, params: Record<string, unknown>, expectedGeneration?: number) => {
      return store.prepareConfirmation({
        operation: op,
        resource_id: resourceId,
        ...(expectedGeneration !== undefined ? { expected_generation: expectedGeneration } : {}),
        params,
      });
    },
    [],
  );

  const clearError = useCallback(() => setState((s) => (s.error ? { ...s, error: null } : s)), []);

  return { run, runPane, prepareConfirmation, clearError, ...state };
}
