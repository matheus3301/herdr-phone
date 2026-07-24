/**
 * Current-pane selection (drives the terminal + ribbon). Stored as just a pane
 * id; workspace/tab are always derived from the live snapshot so the selection
 * survives reorders and heals when a pane closes or its ID changes (SPEC §11,
 * §14.2). External store for useSyncExternalStore.
 */
import type { Pane, Snapshot, Tab, Workspace } from "./types";

let selectedPaneId: string | null = null;
const listeners = new Set<() => void>();

export const selectionStore = {
  subscribe(cb: () => void): () => void {
    listeners.add(cb);
    return () => listeners.delete(cb);
  },
  get(): string | null {
    return selectedPaneId;
  },
  set(paneId: string | null): void {
    if (paneId === selectedPaneId) return;
    selectedPaneId = paneId;
    for (const cb of listeners) cb();
  },
};

export interface ResolvedSelection {
  workspace: Workspace | null;
  tab: Tab | null;
  pane: Pane | null;
}

/**
 * Resolve the effective selection against a snapshot. Falls back, in order, to
 * the selected pane, the focused pane, or the first pane of the focused tab.
 */
export function resolveSelection(snapshot: Snapshot | null, selected: string | null): ResolvedSelection {
  if (!snapshot) return { workspace: null, tab: null, pane: null };

  const byId = (id: string | null) => snapshot.panes.find((p) => p.id === id) ?? null;
  let pane = byId(selected);
  if (!pane) pane = byId(snapshot.focusedPaneId);
  if (!pane) pane = snapshot.panes[0] ?? null;
  if (!pane) return { workspace: null, tab: null, pane: null };

  const tab = snapshot.tabs.find((t) => t.id === pane.tabId) ?? null;
  const workspace = snapshot.workspaces.find((w) => w.id === pane.workspaceId) ?? null;
  return { workspace, tab, pane };
}
