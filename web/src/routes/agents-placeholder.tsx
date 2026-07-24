import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { useRuns } from "@/hooks/use-runs";
import { attentionCount } from "@/lib/run";

/**
 * The detail column's index state.
 *
 * On a phone this is never visible — the shell shows the inbox alone at `/`. At
 * desktop width the inbox is a permanent left column, so the detail column needs
 * something to say before a run is chosen.
 */
export function AgentsPlaceholder() {
  const runs = useRuns();
  const attention = attentionCount(runs);

  return (
    <div className="flex min-h-0 flex-1 items-center justify-center px-8">
      <div className="max-w-sm text-center">
        <p className="text-prose text-mist">
          {attention > 0
            ? `${attention} ${attention === 1 ? "run needs" : "runs need"} a decision.`
            : "Choose a run to supervise it."}
        </p>
        <p className="mt-1 text-body text-muted-ink">
          Runs are listed by urgency on the left. Opening one reads its state — it does not move focus on your Mac.
        </p>
        <Button asChild variant="outline" className="mt-4">
          <Link to="/runs/new">Start a run</Link>
        </Button>
      </div>
    </div>
  );
}
