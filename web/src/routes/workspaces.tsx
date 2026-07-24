import { useMemo } from "react";
import { Link } from "react-router-dom";
import { ChevronRight, GitBranch, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { CreateWorkspaceSheet } from "@/components/create-sheets";
import { WorktreeSheet } from "@/components/worktree-sheet";
import { StatusPill } from "@/components/status-pill";
import { useAppState } from "@/hooks/use-app-store";
import { useRuns } from "@/hooks/use-runs";
import { useRouteTitle } from "@/hooks/use-route-title";
import { shortPath } from "@/lib/format";

/**
 * Workspaces — the secondary inspect-and-manage surface.
 *
 * The first level is about project identity and what is running there, not raw
 * tab and pane counts. Topology lives one level down, behind explicit advanced
 * controls, because it is execution context rather than the product's subject.
 */
export function WorkspacesRoute() {
  const heading = useRouteTitle("Workspaces");
  const { snapshot } = useAppState();
  const runs = useRuns();
  const workspaces = snapshot?.workspaces ?? [];

  const runsByWorkspace = useMemo(() => {
    const map = new Map<string, typeof runs>();
    for (const run of runs) {
      const list = map.get(run.workspaceId) ?? [];
      list.push(run);
      map.set(run.workspaceId, list);
    }
    return map;
  }, [runs]);

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="flex items-center gap-2 px-4 py-3">
        <h1 ref={heading} tabIndex={-1} className="text-prose font-semibold text-mist">
          Workspaces
        </h1>
        <div className="ml-auto flex items-center gap-1">
          <WorktreeSheet trigger={<Button variant="quiet" size="sm">Worktrees</Button>} />
          <CreateWorkspaceSheet
            trigger={
              <Button variant="primary" size="sm">
                <Plus className="size-4" /> New
              </Button>
            }
          />
        </div>
      </div>

      {workspaces.length === 0 ? (
        <div className="px-4 py-10 text-center">
          <p className="text-body text-mist">No workspaces are open.</p>
          <p className="mt-1 text-meta text-muted-ink">
            A workspace is a project directory Herdr is holding open on your Mac.
          </p>
        </div>
      ) : (
        <ul className="divide-y divide-seam border-y border-seam">
          {workspaces.map((workspace) => {
            const active = runsByWorkspace.get(workspace.id) ?? [];
            return (
              <li key={workspace.id}>
                <Link
                  to={`/workspaces/${encodeURIComponent(workspace.id)}`}
                  className="flex min-h-11 items-center gap-3 px-4 py-3 hover:bg-bulkhead focus-visible:outline-2 focus-visible:outline-brass focus-visible:outline-offset-[-2px]"
                >
                  <span className="min-w-0 flex-1">
                    <span className="flex items-baseline gap-2">
                      <span className="truncate text-body font-semibold text-mist">{workspace.label}</span>
                      {workspace.focused && <span className="text-meta text-brass">focused on Mac</span>}
                    </span>
                    {workspace.worktree && (
                      <span className="tabular mt-0.5 flex items-center gap-1.5 text-faint-ink">
                        <GitBranch className="size-3.5 shrink-0" aria-hidden />
                        <span className="truncate" title={workspace.worktree.path}>
                          {workspace.worktree.branch ?? shortPath(workspace.worktree.path, 2)}
                        </span>
                      </span>
                    )}
                    <span className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5">
                      {active.length === 0 ? (
                        <span className="text-meta text-muted-ink">No agents running</span>
                      ) : (
                        active.slice(0, 3).map((run) => (
                          <span key={run.id} className="flex items-center gap-1.5">
                            <StatusPill status={run.status} showLabel={false} />
                            <span className="text-meta text-muted-ink">{run.agentName}</span>
                          </span>
                        ))
                      )}
                      {active.length > 3 && <span className="text-meta text-faint-ink">+{active.length - 3} more</span>}
                    </span>
                  </span>
                  <ChevronRight className="size-5 shrink-0 text-faint-ink" aria-hidden />
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
