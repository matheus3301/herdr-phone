import type { MutationOperation } from "./types";

/**
 * Which mutations require a server confirmation nonce + AlertDialog (SPEC §14.4,
 * §15). Exact match to internal/server/mutations.go `confirmable`, minus
 * terminal.takeover (handled in the terminal view). The backend exposes no
 * worktree "dirty" flag, so a forced removal is a distinct operation
 * (worktree.remove_force) the user escalates to when a plain remove is refused,
 * not a `force:true` param on a dirty-detected worktree.
 */
const CONFIRM_OPS = new Set<MutationOperation>([
  "workspace.close",
  "tab.close",
  "pane.close",
  "worktree.remove",
  "worktree.remove_force",
]);

export function requiresConfirmation(op: MutationOperation): boolean {
  return CONFIRM_OPS.has(op);
}

/** Human summary shown in the confirm dialog (the backend returns no summary). */
export function fallbackSummary(op: MutationOperation, label: string): string {
  switch (op) {
    case "workspace.close":
      return `Close workspace "${label}" and every tab and pane inside it? This cannot be undone.`;
    case "tab.close":
      return `Close tab "${label}"? Every pane in the tab is terminated.`;
    case "pane.close":
      return `Close pane "${label}"? Its process is terminated.`;
    case "worktree.remove":
      return `Remove worktree "${label}"? The checkout is detached from Herdr.`;
    case "worktree.remove_force":
      return `Force-remove worktree "${label}"? This discards uncommitted changes permanently.`;
    default:
      return `Confirm ${op} on "${label}"?`;
  }
}
