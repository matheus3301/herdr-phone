import { useMemo } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  ChevronLeft,
  ChevronLeftCircle,
  ChevronRightCircle,
  Crosshair,
  Ellipsis,
  GitBranch,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { RenameDialog } from "@/components/rename-dialog";
import { ConfirmAction } from "@/components/confirm-action";
import { CreateTabSheet } from "@/components/create-sheets";
import { PaneActions } from "@/components/pane-actions";
import { StatusPill } from "@/components/status-pill";
import { useAppState } from "@/hooks/use-app-store";
import { useMutations } from "@/hooks/use-mutations";
import { useRouteTitle } from "@/hooks/use-route-title";
import { formatRunId } from "@/lib/run";
import { shortPath } from "@/lib/format";
import type { Pane, Tab } from "@/lib/types";

function PaneRow({ pane }: { pane: Pane }) {
  const label = pane.agentName ?? pane.title ?? "shell";
  const runId = pane.agentKind && pane.generation > 0 ? formatRunId({ paneId: pane.id, generation: pane.generation }) : null;

  return (
    <li className="flex items-center gap-2 py-1.5">
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="truncate text-body text-mist">{label}</span>
          {pane.agentStatus && <StatusPill status={pane.agentStatus} showLabel={false} />}
          {!pane.agentKind && <span className="text-meta text-faint-ink">empty shell</span>}
        </span>
        <span className="tabular block truncate text-faint-ink">
          {pane.id} · generation {pane.generation > 0 ? pane.generation : "unknown"}
          {pane.zoomed ? " · zoomed" : ""}
        </span>
      </span>
      {runId && (
        <Button asChild variant="quiet" size="sm">
          <Link to={`/runs/${encodeURIComponent(runId)}`}>Run</Link>
        </Button>
      )}
      <PaneActions
        pane={pane}
        trigger={
          <Button variant="quiet" size="icon" aria-label={`Actions for ${label}`}>
            <Ellipsis className="size-5" />
          </Button>
        }
      />
    </li>
  );
}

function TabSection({ tab, panes, tabsInWorkspace }: { tab: Tab; panes: Pane[]; tabsInWorkspace: Tab[] }) {
  const { run } = useMutations();
  const index = tabsInWorkspace.findIndex((t) => t.id === tab.id);
  const canMoveLeft = index > 0;
  const canMoveRight = index >= 0 && index < tabsInWorkspace.length - 1;

  return (
    <section className="border-t border-seam px-4 py-2">
      <div className="flex items-center gap-1">
        <h3 className="min-w-0 flex-1 truncate text-body font-medium text-mist">
          {tab.label}
          {tab.active && <span className="ml-2 text-meta text-brass">active</span>}
        </h3>
        <Button
          variant="quiet"
          size="icon"
          aria-label={`Move ${tab.label} left`}
          disabled={!canMoveLeft}
          onClick={() => void run("tab.move", { tab_id: tab.id, insert_index: index - 1 })}
        >
          <ChevronLeftCircle className="size-4" />
        </Button>
        <Button
          variant="quiet"
          size="icon"
          aria-label={`Move ${tab.label} right`}
          disabled={!canMoveRight}
          onClick={() => void run("tab.move", { tab_id: tab.id, insert_index: index + 1 })}
        >
          <ChevronRightCircle className="size-4" />
        </Button>
        <Button variant="quiet" size="icon" aria-label={`Focus ${tab.label}`} onClick={() => void run("tab.focus", { tab_id: tab.id })}>
          <Crosshair className="size-4" />
        </Button>
        <RenameDialog
          title="Rename tab"
          operation="tab.rename"
          idKey="tab_id"
          idValue={tab.id}
          current={tab.label}
          trigger={
            <Button variant="quiet" size="icon" aria-label={`Rename ${tab.label}`}>
              <Pencil className="size-4" />
            </Button>
          }
        />
        <ConfirmAction
          operation="tab.close"
          resourceId={tab.id}
          label={tab.label}
          params={{ tab_id: tab.id }}
          trigger={
            <Button variant="quiet" size="icon" aria-label={`Close ${tab.label}`}>
              <Trash2 className="size-4 text-flare" />
            </Button>
          }
        />
      </div>
      <ul className="divide-y divide-seam">
        {panes.map((pane) => (
          <PaneRow key={pane.id} pane={pane} />
        ))}
        {panes.length === 0 && <li className="py-2 text-meta text-muted-ink">This tab has no panes.</li>}
      </ul>
    </section>
  );
}

/**
 * Workspace detail — the full advanced topology surface.
 *
 * Everything the terminal-first UI put on every screen lives here instead: tabs,
 * panes, empty shells, lifecycle generations, layout, rename, move, split,
 * close, and worktree removal. It is one level down from the workspace list
 * because it is execution context, not the product's subject.
 */
export function WorkspaceDetailRoute() {
  const { workspaceId = "" } = useParams();
  const navigate = useNavigate();
  const { snapshot } = useAppState();
  const { run } = useMutations();
  const workspace = snapshot?.workspaces.find((w) => w.id === workspaceId) ?? null;
  const heading = useRouteTitle(workspace?.label ?? "Workspace");

  const tabs = useMemo(
    () => (snapshot?.tabs ?? []).filter((t) => t.workspaceId === workspaceId).sort((a, b) => a.order - b.order),
    [snapshot, workspaceId],
  );
  const panesByTab = useMemo(() => {
    const map = new Map<string, Pane[]>();
    for (const pane of snapshot?.panes ?? []) {
      if (pane.workspaceId !== workspaceId) continue;
      const list = map.get(pane.tabId) ?? [];
      list.push(pane);
      map.set(pane.tabId, list);
    }
    for (const list of map.values()) list.sort((a, b) => a.order - b.order);
    return map;
  }, [snapshot, workspaceId]);

  // Provenance comes from the workspace itself — the only place a snapshot
  // carries it. `worktree.remove` takes the *workspace* a worktree is open in,
  // and git refuses to remove a main checkout, so a linked worktree is exactly
  // the removable case: the control is offered when, and only when, it can work.
  const worktree = workspace?.worktree ?? null;

  if (!workspace) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-6">
        <h1 ref={heading} tabIndex={-1} className="text-prose font-semibold text-mist">
          Workspace not found
        </h1>
        <p className="mt-1 text-body text-muted-ink">It was closed on your Mac, or the id no longer resolves.</p>
        <Button asChild variant="outline" className="mt-4">
          <Link to="/workspaces">Back to workspaces</Link>
        </Button>
      </div>
    );
  }

  return (
    <div className="min-h-0 flex-1 overflow-y-auto pb-8">
      <div className="flex items-center gap-1 px-2 pt-1">
        <Button variant="quiet" size="icon" aria-label="Back to workspaces" onClick={() => navigate("/workspaces")}>
          <ChevronLeft className="size-5" />
        </Button>
        <h1 ref={heading} tabIndex={-1} className="min-w-0 flex-1 truncate text-prose font-semibold text-mist">
          {workspace.label}
        </h1>
      </div>

      <div className="px-4 py-2">
        {worktree && (
          <p className="tabular flex items-center gap-1.5 text-faint-ink">
            <GitBranch className="size-3.5 shrink-0" aria-hidden />
            <span className="truncate" title={worktree.checkoutPath}>
              {worktree.repoName}
              {worktree.isLinkedWorktree ? " · linked worktree" : " · main checkout"} ·{" "}
              {shortPath(worktree.checkoutPath, 3)}
            </span>
          </p>
        )}
        <p className="tabular mt-0.5 text-faint-ink">
          {workspace.id} · {workspace.tabCount} tabs · {workspace.paneCount} panes
        </p>

        <div className="mt-3 flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => void run("workspace.focus", { workspace_id: workspace.id })}>
            <Crosshair className="size-4" /> Focus on Mac
          </Button>
          <RenameDialog
            title="Rename workspace"
            operation="workspace.rename"
            idKey="workspace_id"
            idValue={workspace.id}
            current={workspace.label}
            trigger={
              <Button variant="outline" size="sm">
                <Pencil className="size-4" /> Rename
              </Button>
            }
          />
          <CreateTabSheet
            workspaceId={workspace.id}
            trigger={
              <Button variant="outline" size="sm">
                <Plus className="size-4" /> New tab
              </Button>
            }
          />
        </div>
      </div>

      <div className="mt-1">
        <h2 className="px-4 pb-1 text-body font-semibold text-mist">Tabs and panes</h2>
        {tabs.map((tab) => (
          <TabSection key={tab.id} tab={tab} panes={panesByTab.get(tab.id) ?? []} tabsInWorkspace={tabs} />
        ))}
      </div>

      <div className="mt-4 border-t border-seam px-4 pt-3">
        <Collapsible>
          <CollapsibleTrigger className="text-muted-ink hover:text-mist">Danger zone</CollapsibleTrigger>
          <CollapsibleContent>
            <div className="flex flex-col gap-2 pb-2 pl-6">
              <p className="text-meta text-muted-ink">
                Closing a workspace terminates every pane inside it. Removing a worktree detaches the checkout from
                Herdr; forcing it discards uncommitted work.
              </p>
              <div className="flex flex-wrap gap-2">
                <ConfirmAction
                  operation="workspace.close"
                  resourceId={workspace.id}
                  label={workspace.label}
                  params={{ workspace_id: workspace.id }}
                  onDone={() => navigate("/workspaces")}
                  trigger={
                    <Button variant="outline" size="sm" className="text-flare">
                      <Trash2 className="size-4" /> Close workspace
                    </Button>
                  }
                />
                {worktree?.isLinkedWorktree && (
                  <ConfirmAction
                    operation="worktree.remove"
                    resourceId={workspace.id}
                    label={worktree.repoName}
                    params={{ worktree_id: workspace.id }}
                    escalateOperation="worktree.remove_force"
                    onDone={() => navigate("/workspaces")}
                    trigger={
                      <Button variant="outline" size="sm" className="text-flare">
                        <Trash2 className="size-4" /> Remove worktree
                      </Button>
                    }
                  />
                )}
              </div>
            </div>
          </CollapsibleContent>
        </Collapsible>
      </div>
    </div>
  );
}
