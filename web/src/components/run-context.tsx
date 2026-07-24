import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { shortPath } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Run } from "@/lib/run";

/**
 * Exact execution context, collapsed to one line and expandable in place.
 *
 * The phone hides the topology during the normal journey, but it never pretends
 * the topology does not exist: before an instruction or a destructive action the
 * operator can see precisely which workspace, worktree, tab, pane, generation,
 * and agent they are about to affect.
 */
export function RunContext({ run, className, defaultOpen = false }: { run: Run; className?: string; defaultOpen?: boolean }) {
  // workspace / worktree / tab / agent, minus repeats — a worktree branch and
  // the tab opened for it very often share a name, and printing it twice makes
  // the line look broken rather than precise.
  const summary = [run.workspaceLabel, run.worktreeBranch, run.tabLabel, run.agentName]
    .filter((part, index, all): part is string => !!part && all.indexOf(part) === index)
    .join(" / ");

  return (
    <Collapsible defaultOpen={defaultOpen} className={cn("text-meta", className)}>
      <CollapsibleTrigger className="min-h-11 px-1 py-1 text-muted-ink hover:text-mist">
        <span className="tabular truncate">{summary}</span>
        <span className="sr-only">Show full execution context</span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 px-1 pb-2 pt-1">
          <Row label="Workspace" value={run.workspaceLabel} />
          {run.worktreeBranch && <Row label="Worktree" value={run.worktreeBranch} />}
          {run.worktreePath && <Row label="Checkout" value={shortPath(run.worktreePath, 3)} title={run.worktreePath} />}
          <Row label="Tab" value={run.tabLabel} />
          <Row label="Pane" value={run.paneId} />
          <Row label="Generation" value={run.generation > 0 ? String(run.generation) : "unknown"} />
          <Row label="Agent" value={`${run.agentName} (${run.agentKind})`} />
          <Row label="Directory" value={shortPath(run.cwd, 3)} title={run.cwd} />
        </dl>
      </CollapsibleContent>
    </Collapsible>
  );
}

function Row({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <>
      <dt className="text-faint-ink">{label}</dt>
      <dd className="tabular min-w-0 truncate text-mist" title={title ?? value}>
        {value}
      </dd>
    </>
  );
}
