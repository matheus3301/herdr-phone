import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { RefreshCw, SquareTerminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Skeleton } from "@/components/ui/skeleton";
import { useRunAdapter } from "@/hooks/use-runs";
import { RECENT_OUTPUT_LINES, type TerminalTail as Tail } from "@/lib/run-adapter";
import { relativeTime, sanitizeBlock } from "@/lib/format";
import type { Run } from "@/lib/run";

/**
 * Recent terminal output — the honest fallback.
 *
 * The relay has no structured message contract, so this is exactly what it says
 * it is: a bounded tail of what the pane last rendered. It is never styled as an
 * assistant message, never parsed for tool calls or approvals, and it is always
 * accompanied by a one-tap route to the full console, which is the only surface
 * that shows the pane faithfully.
 *
 * Content is rendered as text into a `<pre>` (never innerHTML) with C0/C1
 * controls stripped, and it is fetched with `no-store` — nothing here is cached
 * by the service worker or written to storage.
 */
export function TerminalTail({ run, now }: { run: Run; now: number }) {
  const adapter = useRunAdapter();
  const [tail, setTail] = useState<Tail | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      const result = await adapter.readRecentOutput(run.paneId, RECENT_OUTPUT_LINES, signal);
      if (signal?.aborted) return;
      setTail(result);
      setFailed(result === null);
      setLoading(false);
    },
    [adapter, run.paneId],
  );

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const text = tail ? sanitizeBlock(tail.content).trimEnd() : "";

  return (
    <section aria-labelledby="recent-output-heading" className="rounded-log bg-hull">
      <Collapsible defaultOpen>
        <div className="flex items-center gap-2 px-3 py-2">
          <CollapsibleTrigger className="min-h-11 flex-1">
            <span id="recent-output-heading" className="text-body font-semibold text-mist">
              Recent terminal output
            </span>
          </CollapsibleTrigger>
          <Button variant="quiet" size="icon" aria-label="Refresh recent output" onClick={() => void load()}>
            <RefreshCw className="size-4" />
          </Button>
        </div>
        <CollapsibleContent>
          <p className="px-3 pb-2 text-meta text-muted-ink">
            The last {RECENT_OUTPUT_LINES} lines this pane rendered. Not a transcript, and not the agent's own
            messages — Herdr does not publish those yet.
          </p>
          <div className="px-3 pb-3">
            {loading && !tail ? (
              <Skeleton className="h-24 w-full" />
            ) : failed ? (
              <p className="text-meta text-muted-ink">
                Herdr could not read this pane. Open the console to see what it is doing.
              </p>
            ) : text ? (
              <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-log bg-terminal p-3 font-mono text-[12.5px] leading-relaxed text-terminal-ink">
                {text}
              </pre>
            ) : (
              <p className="text-meta text-muted-ink">This pane has rendered nothing recently.</p>
            )}
            {tail && (
              <p className="tabular mt-1.5 text-faint-ink">read {relativeTime(tail.readAt, now)}</p>
            )}
          </div>
          <div className="border-t border-seam px-3 py-2">
            <Button asChild variant="outline" size="sm">
              <Link to={`/console/${encodeURIComponent(run.paneId)}?generation=${run.generation}`}>
                <SquareTerminal className="size-4" /> Open console
              </Link>
            </Button>
          </div>
        </CollapsibleContent>
      </Collapsible>
    </section>
  );
}
