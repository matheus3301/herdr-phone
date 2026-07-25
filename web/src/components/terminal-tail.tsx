import { useEffect } from "react";
import { Link } from "react-router-dom";
import { RefreshCw, SquareTerminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Skeleton } from "@/components/ui/skeleton";
import { useRunAdapter, useRunOutput } from "@/hooks/use-runs";
import { relativeTime, sanitizeBlock } from "@/lib/format";
import type { Run } from "@/lib/run";

/**
 * Recent terminal output.
 *
 * Both sources end up here and both are labelled the same way, because both are
 * the same thing: bytes a pane rendered.
 *
 *  - On a relay with the structured run contract this is the
 *    `observed_terminal_output` part of `GET /runs/{pane_id}` — a typed part
 *    that carries no role. The relay's other advertised capabilities are all
 *    false, so there is nothing else to render.
 *  - On an older relay it is a bounded `pane.read`.
 *
 * It is never styled as an assistant message, never parsed for tool calls,
 * approvals, diffs, or test results, and it always sits next to a one-tap route
 * to the full console, which is the only surface that shows the pane faithfully.
 *
 * Content is rendered as text into a `<pre>` (never innerHTML) with C0/C1
 * controls stripped, and it is fetched with `no-store` — nothing here is cached
 * by the service worker or written to storage.
 */
export function TerminalTail({
  run,
  now,
  onInvalidated,
}: {
  run: Run;
  now: number;
  /** Called when the relay reports the run's own identity is no longer valid. */
  onInvalidated?: () => void;
}) {
  const adapter = useRunAdapter();
  const { result, loading, reload } = useRunOutput(run);

  const failed = result?.kind === "error" ? result : null;
  const output = result?.kind === "ok" ? result.output : null;
  const invalidated = !!failed?.invalidates;

  useEffect(() => {
    if (invalidated) onInvalidated?.();
  }, [invalidated, onInvalidated]);

  const text = output ? sanitizeBlock(output.text).trimEnd() : "";
  const lineWord = output?.lines === 1 ? "line" : "lines";

  return (
    <section
      aria-labelledby="recent-output-heading"
      // Explicitly silent. Every refresh replaces this whole block's text, which
      // a polite ancestor would treat as an addition and read out in full — up to
      // 40 lines of terminal bytes, on every refresh. Declaring `off` here means
      // the region cannot be re-announced even if an ancestor becomes live later.
      aria-live="off"
      className="rounded-log bg-hull"
    >
      <Collapsible defaultOpen>
        <div className="flex items-center gap-2 px-3 py-2">
          <CollapsibleTrigger className="min-h-11 flex-1">
            <span id="recent-output-heading" className="text-body font-semibold text-mist">
              Recent terminal output
            </span>
          </CollapsibleTrigger>
          <Button variant="quiet" size="icon" aria-label="Refresh recent output" onClick={reload}>
            <RefreshCw className="size-4" />
          </Button>
        </div>
        <CollapsibleContent>
          {output && (
            <p className="px-3 pb-2 text-meta text-muted-ink">
              {adapter.usesRunContract
                ? `The last ${output.lines} ${lineWord} this pane rendered, as the relay's run contract reports them. Not a transcript, and not the agent's own messages — Herdr does not publish those.`
                : `The last ${output.lines} ${lineWord} this pane rendered. Not a transcript, and not the agent's own messages — Herdr does not publish those yet.`}
            </p>
          )}
          <div className="px-3 pb-3">
            {loading && !output ? (
              <Skeleton className="h-24 w-full" />
            ) : failed ? (
              <p className="text-meta text-muted-ink">
                {failed.message} Open the console to see what this pane is doing.
              </p>
            ) : text ? (
              <>
                {output?.truncated && (
                  <p className="mb-1.5 text-meta text-muted-ink">
                    Older output was dropped to fit the relay's size limit. The console has the full scrollback.
                  </p>
                )}
                <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-log bg-terminal p-3 font-mono text-[12.5px] leading-relaxed text-terminal-ink">
                  {text}
                </pre>
              </>
            ) : (
              <p className="text-meta text-muted-ink">This pane has rendered nothing recently.</p>
            )}
            {output && (
              <p className="tabular mt-1.5 text-faint-ink">
                {output.source} · read {relativeTime(output.readAt, now)}
              </p>
            )}
            {output && output.ignoredPartTypes.length > 0 && (
              // The relay may add part types in a future build without bumping
              // the contract version. An unknown part is counted, never guessed at.
              <p className="mt-1 text-meta text-muted-ink">
                {output.ignoredPartTypes.length === 1
                  ? "1 part this app does not understand was not shown."
                  : `${output.ignoredPartTypes.length} parts this app does not understand were not shown.`}
              </p>
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
