/**
 * Canonical pane-scoped mutation parameters.
 *
 * The relay's allowlist (internal/server/mutations.go) guards every `pane.*` and
 * `agent.*` operation on the pane lifecycle generation, and the guard is
 * mandatory: live generations start at 1, so a missing or zero
 * `expected_generation` is rejected outright. Several agent dispatchers also
 * prefer an alternate `target` field over `pane_id`, and the server rejects a
 * request whose `target` diverges from the canonical resource — so the client
 * never sends `target` at all and always keys on the immutable pane id.
 *
 * Everything that mutates a pane or an agent goes through this module, so the
 * contract is stated once and testable in isolation.
 */

/** Operations the relay guards on `pane_id` + `expected_generation`. */
export const PANE_SCOPED_OPERATIONS = [
  "pane.focus",
  "pane.split",
  "pane.resize",
  "pane.zoom",
  "pane.swap",
  "pane.move",
  "pane.rename",
  "pane.close",
  "agent.focus",
  "agent.prompt",
  "agent.send_keys",
  "agent.rename",
  "agent.start",
] as const;

export type PaneScopedOperation = (typeof PANE_SCOPED_OPERATIONS)[number];

const PANE_SCOPED = new Set<string>(PANE_SCOPED_OPERATIONS);

export function isPaneScoped(operation: string): operation is PaneScopedOperation {
  return PANE_SCOPED.has(operation);
}

/** Identifiers a dispatcher would prefer over `pane_id`. Never sent. */
export const FORBIDDEN_PANE_ALIASES = ["target", "agent", "name_target"] as const;

export interface PaneTarget {
  paneId: string;
  generation: number;
}

export interface GenerationProblem {
  code: "generation_missing";
  message: string;
}

/**
 * Validate a pane target before any network call. A generation of 0 means the
 * snapshot has no lifecycle entry for the pane — the pane is gone, or the
 * snapshot is stale. Either way the request would be refused by the relay, so
 * it is refused here with a message the user can act on.
 */
export function checkPaneTarget(target: PaneTarget | null | undefined): GenerationProblem | null {
  if (!target || !target.paneId) {
    return { code: "generation_missing", message: "No pane is bound to this action yet." };
  }
  if (!Number.isInteger(target.generation) || target.generation <= 0) {
    return {
      code: "generation_missing",
      message: "This pane's lifecycle generation is unknown, so the change was not sent. Reload the herd and try again.",
    };
  }
  return null;
}

/**
 * Build params for a pane-scoped mutation: the canonical `pane_id` plus the
 * caller's fields, with any dispatcher-preferred alias stripped so the guard and
 * the dispatch can never key on different identifiers.
 */
export function paneParams(target: PaneTarget, extra: Record<string, unknown> = {}): Record<string, unknown> {
  const params: Record<string, unknown> = { pane_id: target.paneId };
  for (const [key, value] of Object.entries(extra)) {
    if ((FORBIDDEN_PANE_ALIASES as readonly string[]).includes(key)) continue;
    if (key === "pane_id") continue;
    if (value === undefined) continue;
    params[key] = value;
  }
  return params;
}
