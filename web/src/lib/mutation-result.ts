/**
 * Readers for mutation `result` payloads (internal/herdr/mutations.go). Creation
 * results expose next-step objects, not bare ids: workspace.create / tab.create →
 * `result.root_pane.pane_id`; pane.split → `result.pane.pane_id`.
 */
import type { MutationResponse } from "./types";

function resultObj(res: MutationResponse | null): Record<string, unknown> | null {
  if (!res || "error" in res) return null;
  const r = (res as { result?: unknown }).result;
  return r && typeof r === "object" ? (r as Record<string, unknown>) : null;
}

function paneIdOf(v: unknown): string | null {
  if (v && typeof v === "object" && typeof (v as { pane_id?: unknown }).pane_id === "string") {
    return (v as { pane_id: string }).pane_id;
  }
  return null;
}

/** The root pane id from a workspace.create / tab.create result. */
export function rootPaneId(res: MutationResponse | null): string | null {
  const r = resultObj(res);
  return r ? paneIdOf(r.root_pane) : null;
}

/** The new pane id from a pane.split result. */
export function splitPaneId(res: MutationResponse | null): string | null {
  const r = resultObj(res);
  return r ? paneIdOf(r.pane) : null;
}
