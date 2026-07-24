import { useEffect, useRef, useState } from "react";
import { Link, NavLink } from "react-router-dom";
import { GitBranch, Plus } from "lucide-react";
import { StatusPill } from "@/components/status-pill";
import { Button } from "@/components/ui/button";
import { useRunGroups, useRunList } from "@/hooks/use-runs";
import { useNow } from "@/hooks/use-now";
import { runStore } from "@/lib/run-store";
import { attentionCount, type Run, type RunGroup } from "@/lib/run";
import { stabilizeGroups } from "@/lib/stable-order";
import { seenLabel } from "@/lib/format";
import { cn } from "@/lib/utils";

interface SteadyHandlers {
  onPointerDown: () => void;
  onPointerUp: () => void;
  onPointerCancel: () => void;
  onFocusCapture: () => void;
  onBlurCapture: () => void;
}

/**
 * Hold the sectioned inbox steady while it is being touched, so a status change
 * can never slide a row out from under a tap. Content still refreshes in place.
 */
function useSteadyGroups(groups: RunGroup[]): { groups: RunGroup[]; handlers: SteadyHandlers } {
  // The frozen reference is state, not a ref: it is read during render, and it
  // is captured at the moment interaction starts.
  const [frozen, setFrozen] = useState<RunGroup[] | null>(null);
  const pointer = useRef(false);
  const focused = useRef(false);

  const shown = frozen ? stabilizeGroups(frozen, groups) : groups;

  // Handlers are recreated each render, so they close over the live `groups`.
  const sync = () => {
    const active = pointer.current || focused.current;
    setFrozen(active ? (frozen ?? groups) : null);
  };

  return {
    groups: shown,
    handlers: {
      onPointerDown: () => {
        pointer.current = true;
        sync();
      },
      onPointerUp: () => {
        pointer.current = false;
        sync();
      },
      onPointerCancel: () => {
        pointer.current = false;
        sync();
      },
      onFocusCapture: () => {
        focused.current = true;
        sync();
      },
      onBlurCapture: () => {
        focused.current = false;
        sync();
      },
    },
  };
}

function RunRow({ run, now }: { run: Run; now: number }) {
  const seen = seenLabel(runStore.lastSeenAt(run.id), now);
  const attention = run.section === "attention";

  return (
    <li>
      <NavLink
        to={`/runs/${encodeURIComponent(run.id)}`}
        className={({ isActive }) =>
          cn(
            "@container block px-4 py-3 transition-colors",
            "focus-visible:outline-2 focus-visible:outline-brass focus-visible:outline-offset-[-2px]",
            isActive ? "bg-brass/10" : "hover:bg-bulkhead",
          )
        }
      >
        <span
          className={cn(
            "flex flex-col gap-1 border-l-2 pl-3",
            attention ? "border-flare" : "border-transparent",
          )}
        >
          <span className="flex min-w-0 items-baseline gap-2">
            <span className="truncate text-body font-semibold text-mist">{run.agentName}</span>
            <span className="tabular shrink-0 text-faint-ink">{run.agentKind}</span>
          </span>

          {/* Herdr's own normalized pane title — metadata, never parsed output. */}
          {run.terminalTitle && <span className="line-clamp-2 text-body text-muted-ink">{run.terminalTitle}</span>}

          <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
            <StatusPill status={run.status} />
            <span className="tabular truncate text-faint-ink" title={run.cwd}>
              {run.workspaceLabel}
              {run.worktreeBranch ? ` / ${run.worktreeBranch}` : ""}
            </span>
            {seen && <span className="tabular shrink-0 text-faint-ink">{seen}</span>}
          </span>
        </span>
      </NavLink>
    </li>
  );
}

/**
 * The attention inbox — the product's default surface.
 *
 * Sections are ordered by urgency and never merged: `done` reads as Updated
 * because Herdr reports only that background work settled, and `unknown` gets
 * its own section rather than being hidden among idle agents. Opening a row is
 * read-only with respect to Herdr focus — it navigates, it does not call
 * `agent.focus`.
 */
export function RunInbox() {
  const { runs, truncated, maxRuns, loading, error } = useRunList();
  const liveGroups = useRunGroups(runs);
  const { groups, handlers } = useSteadyGroups(liveGroups);
  const now = useNow(15_000);

  const attention = attentionCount(runs);
  const [announcement, setAnnouncement] = useState("");
  const previousAttention = useRef(attention);

  // Announce new attention exactly once. Ordinary status churn must not flood
  // the live region.
  useEffect(() => {
    if (attention > previousAttention.current) {
      setAnnouncement(`${attention} ${attention === 1 ? "run needs" : "runs need"} you`);
    } else if (attention === 0) {
      setAnnouncement("");
    }
    previousAttention.current = attention;
  }, [attention]);

  const total = runs.length;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-2 px-4 py-3">
        <h1 className="text-prose font-semibold text-mist" tabIndex={-1}>
          Agents
        </h1>
        <span className="tabular text-faint-ink">{total}</span>
        <Button asChild variant="primary" size="sm" className="ml-auto">
          <Link to="/runs/new">
            <Plus className="size-4" /> Start run
          </Link>
        </Button>
      </div>

      <p aria-live="polite" className="sr-only">
        {announcement}
      </p>

      {error && (
        <p role="status" className="border-y border-seam bg-hull px-4 py-2 text-meta text-flare">
          {error} Showing the last list this phone received.
        </p>
      )}

      {/* The relay bounds the list. Saying so is the difference between "these
          are all your runs" and "these are the first N of them". */}
      {truncated && (
        <p role="status" className="border-y border-seam bg-hull px-4 py-2 text-meta text-muted-ink">
          The relay returned only the first {maxRuns > 0 ? maxRuns : total} runs. Some runs are not listed here.
        </p>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto pb-6" {...handlers}>
        {total === 0 && loading ? (
          <p className="px-4 py-10 text-center text-body text-muted-ink">Loading runs…</p>
        ) : total === 0 ? (
          <div className="px-4 py-10 text-center">
            <p className="text-body text-mist">No agents are running.</p>
            <p className="mt-1 text-meta text-muted-ink">
              Start a run to create the workspace, launch an agent, and send it an objective.
            </p>
            <Button asChild variant="primary" className="mt-4">
              <Link to="/runs/new">
                <Plus className="size-4" /> Start run
              </Link>
            </Button>
          </div>
        ) : (
          groups.map((group) =>
            group.runs.length === 0 ? null : (
              <section key={group.key} className="pb-1">
                <h2 className="sticky top-0 z-10 flex items-baseline gap-2 bg-deck/95 px-4 py-2 text-meta font-semibold text-muted-ink backdrop-blur-[1px]">
                  {group.title}
                  <span className="tabular text-faint-ink">{group.runs.length}</span>
                </h2>
                <ul className="divide-y divide-seam border-y border-seam">
                  {group.runs.map((run) => (
                    <RunRow key={run.id} run={run} now={now} />
                  ))}
                </ul>
              </section>
            ),
          )
        )}
      </div>

      <WorktreeHint />
    </div>
  );
}

/** A quiet footer pointer to the secondary management surface. */
function WorktreeHint() {
  return (
    <div className="border-t border-seam px-4 py-2">
      <Link
        to="/workspaces"
        className="inline-flex min-h-11 items-center gap-2 text-meta text-muted-ink hover:text-mist focus-visible:outline-2 focus-visible:outline-brass"
      >
        <GitBranch className="size-4" aria-hidden />
        Manage workspaces and worktrees
      </Link>
    </div>
  );
}
