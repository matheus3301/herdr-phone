import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { GitBranch, Pencil, Plus, SquareTerminal, Trash2, GitFork } from "lucide-react";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { CreateWorkspaceSheet } from "@/components/create-sheets";
import { RenameDialog } from "@/components/rename-dialog";
import { ConfirmAction } from "@/components/confirm-action";
import { WorktreeSheet } from "@/components/worktree-sheet";
import { EmptyState } from "@/components/states";
import { useAppState } from "@/hooks/use-app-store";
import { useMutations } from "@/hooks/use-mutations";
import { selectionStore } from "@/lib/selection";
import { shortPath } from "@/lib/format";

export function SpacesRoute() {
  const { snapshot } = useAppState();
  const { run } = useMutations();
  const navigate = useNavigate();
  const workspaces = snapshot?.workspaces ?? [];

  const firstPaneByWorkspace = useMemo(() => {
    const map = new Map<string, string>();
    if (!snapshot) return map;
    for (const w of snapshot.workspaces) {
      const tab = snapshot.tabs.find((t) => t.id === w.activeTabId) ?? snapshot.tabs.find((t) => t.workspaceId === w.id);
      const pane = tab ? snapshot.panes.filter((p) => p.tabId === tab.id).sort((a, b) => a.order - b.order)[0] : undefined;
      if (pane) map.set(w.id, pane.id);
    }
    return map;
  }, [snapshot]);

  function open(workspaceId: string) {
    const paneId = firstPaneByWorkspace.get(workspaceId);
    if (!paneId) return;
    selectionStore.set(paneId);
    void run("workspace.focus", { workspace_id: workspaceId });
    navigate("/");
  }

  if (!snapshot) return null;

  return (
    <div className="h-full overflow-y-auto px-3 py-3">
      <div className="flex items-center justify-between">
        <h1 className="font-utility text-[11px] uppercase tracking-wider text-muted-ink">Spaces</h1>
        <WorktreeSheet
          trigger={
            <Button variant="ghost" size="sm" className="gap-1.5">
              <GitFork className="size-4" /> Worktrees
            </Button>
          }
        />
      </div>

      {workspaces.length === 0 ? (
        <div className="flex h-[60%] items-center justify-center">
          <EmptyState title="No workspaces" description="Create your first workspace to get started." />
        </div>
      ) : (
        <ul className="mt-2 flex flex-col gap-2">
          {workspaces.map((w) => (
            <li key={w.id} className="rounded-[10px] border border-frame bg-hull p-3">
              <div className="flex items-center gap-2">
                <StatusDot status={w.agentStatus} />
                <button
                  type="button"
                  onClick={() => open(w.id)}
                  className="flex-1 truncate text-left text-sm font-medium text-mist focus-visible:outline-2 focus-visible:outline-brass"
                >
                  {w.label}
                </button>
                <span className="font-utility text-[11px] text-muted-ink">
                  {w.tabCount} tabs · {w.paneCount} panes
                </span>
              </div>
              {w.worktree && (
                <p className="mt-1 flex items-center gap-1.5 font-utility text-[11px] text-muted-ink">
                  <GitBranch className="size-3.5" />
                  <span className="truncate">{w.worktree.branch ?? shortPath(w.worktree.path, 2)}</span>
                </p>
              )}
              <div className="mt-2 flex items-center gap-2">
                <Button variant="default" size="sm" className="flex-1" onClick={() => open(w.id)}>
                  <SquareTerminal className="size-4" /> Open
                </Button>
                <RenameDialog
                  title="Rename workspace"
                  operation="workspace.rename"
                  idKey="workspace_id"
                  idValue={w.id}
                  valueKey="label"
                  current={w.label}
                  trigger={
                    <Button variant="ghost" size="icon" aria-label={`Rename ${w.label}`}>
                      <Pencil className="size-4" />
                    </Button>
                  }
                />
                <ConfirmAction
                  operation="workspace.close"
                  resourceId={w.id}
                  label={w.label}
                  params={{ workspace_id: w.id }}
                  trigger={
                    <Button variant="ghost" size="icon" aria-label={`Close ${w.label}`}>
                      <Trash2 className="size-4 text-flare" />
                    </Button>
                  }
                />
              </div>
            </li>
          ))}
        </ul>
      )}

      <CreateWorkspaceSheet
        trigger={
          <Button variant="primary" className="mt-3 w-full justify-center gap-2">
            <Plus className="size-4" /> New workspace
          </Button>
        }
      />
    </div>
  );
}
