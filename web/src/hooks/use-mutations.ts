import { useCallback, useState } from "react";
import { store } from "@/lib/store";
import { ApiError } from "@/lib/api";
import { requiresConfirmation } from "@/lib/confirm";
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

/**
 * Dispatch typed, allowlisted mutations (SPEC §12, §15). Destructive operations
 * must carry a server confirmation nonce; the caller obtains one via
 * `prepareConfirmation` and passes it through the AlertDialog flow.
 */
export function useMutations() {
  const [state, setState] = useState<MutationState>({ pending: false, error: null });

  const run = useCallback(
    async (op: MutationOperation, params: Record<string, unknown>, opts: RunOptions = {}): Promise<MutationResponse | null> => {
      if (requiresConfirmation(op) && !opts.confirmation) {
        setState({ pending: false, error: "This action needs confirmation." });
        return null;
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
        setState({ pending: false, error: err instanceof ApiError ? err.message : "Mutation failed" });
        return null;
      }
    },
    [],
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

  return { run, prepareConfirmation, ...state };
}
