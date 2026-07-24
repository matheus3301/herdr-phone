import { lazy, Suspense, useMemo, useRef } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ChevronLeft } from "lucide-react";
import { KeyDock } from "@/components/key-dock";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAppState } from "@/hooks/use-app-store";
import { usePrefs } from "@/hooks/use-prefs";
import { useRouteTitle } from "@/hooks/use-route-title";
import { useVisualViewport } from "@/hooks/use-visual-viewport";
import { store } from "@/lib/store";
import { checkPaneTarget } from "@/lib/pane-ops";
import { formatRunId } from "@/lib/run";
import { shortPath } from "@/lib/format";
import type { TerminalHandle } from "@/components/terminal-view";

/**
 * xterm.js and its addons are a large dependency that most sessions never need,
 * so the console — and only the console — pulls them in, at the moment the
 * operator asks for it.
 */
const TerminalView = lazy(() =>
  import("@/components/terminal-view").then((m) => ({ default: m.TerminalView })),
);

/**
 * The console: full-fidelity recovery and direct control.
 *
 * This is the expert surface, reachable in one tap from a run or a pane menu but
 * absent from primary navigation. The ANSI-filtered xterm.js view, the logical
 * key dock, resize, conflict detection, and nonce-backed takeover are unchanged
 * — it is never replaced with a `<pre>`-based pseudo-terminal.
 */
export function ConsoleRoute() {
  const { paneId = "" } = useParams();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const { snapshot } = useAppState();
  const prefs = usePrefs();
  const vp = useVisualViewport();
  const termRef = useRef<TerminalHandle | null>(null);

  const pane = useMemo(() => snapshot?.panes.find((p) => p.id === paneId) ?? null, [snapshot, paneId]);
  const heading = useRouteTitle(pane ? `Console · ${pane.agentName ?? pane.title ?? pane.id}` : "Console");

  // The caller may assert the generation it saw; the live snapshot wins, so a
  // pane recycled between navigation and attach is caught here rather than by a
  // rejected upgrade.
  const asserted = Number(params.get("generation") ?? 0);
  const generation = pane?.generation ?? 0;
  const target = { paneId, generation };
  const problem = checkPaneTarget(target);
  const staleAssertion = asserted > 0 && generation > 0 && asserted !== generation;

  const requestTakeover = async (): Promise<string | null> => {
    if (!store.canMutate() || generation <= 0) return null;
    try {
      const confirmation = await store.prepareConfirmation({
        operation: "terminal.takeover",
        resource_id: paneId,
        expected_generation: generation,
        params: {},
      });
      return confirmation.confirmation;
    } catch {
      return null;
    }
  };

  if (!pane || problem) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-6">
        <h1 ref={heading} tabIndex={-1} className="text-prose font-semibold text-mist">
          This pane is not available
        </h1>
        <p className="mt-1 max-w-prose text-body text-muted-ink">
          {problem
            ? problem.message
            : "Herdr has no pane with that id any more. It was closed, or the snapshot has moved on."}
        </p>
        <p className="tabular mt-3 text-faint-ink">{paneId}</p>
        <div className="mt-5 flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => store.revalidate()}>
            Refresh
          </Button>
          <Button asChild variant="quiet">
            <Link to="/">Back to agents</Link>
          </Button>
        </div>
      </div>
    );
  }

  const runId = pane.agentKind ? formatRunId({ paneId: pane.id, generation }) : null;

  return (
    <div className="flex min-h-0 flex-1 flex-col" style={{ paddingBottom: vp.keyboardInset }}>
      <div className="flex items-center gap-1 border-b border-seam bg-deck px-2 py-1">
        <Button variant="quiet" size="icon" aria-label="Back" onClick={() => navigate(-1)}>
          <ChevronLeft className="size-5" />
        </Button>
        <div className="min-w-0 flex-1">
          <h1 ref={heading} tabIndex={-1} className="truncate text-body font-semibold text-mist">
            {pane.agentName ?? pane.title ?? "Console"}
          </h1>
          <p className="tabular truncate text-faint-ink" title={pane.cwd}>
            {pane.id} · generation {generation} · {shortPath(pane.cwd, 2)}
          </p>
        </div>
        {runId && (
          <Button asChild variant="quiet" size="sm">
            <Link to={`/runs/${encodeURIComponent(runId)}`}>Run</Link>
          </Button>
        )}
      </div>

      {staleAssertion && (
        <p className="bg-brass/15 px-4 py-1.5 text-meta text-brass" role="status">
          This pane was replaced since you opened the link. You are attached to generation {generation}.
        </p>
      )}

      <div className="min-h-0 flex-1">
        <Suspense fallback={<Skeleton className="h-full w-full rounded-none bg-terminal" />}>
          <TerminalView
            key={`${pane.id}:${generation}`}
            ref={termRef}
            paneId={pane.id}
            generation={generation}
            fontSize={prefs.terminalFontSize}
            onRequestTakeover={requestTakeover}
          />
        </Suspense>
      </div>

      <KeyDock
        onChord={(chord) => termRef.current?.sendChord(chord)}
        onPaste={(text) => termRef.current?.paste(text)}
      />
    </div>
  );
}
