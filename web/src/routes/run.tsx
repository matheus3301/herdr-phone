import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowDown, ChevronLeft, Crosshair, Ellipsis, Keyboard, Pencil, SquareTerminal, Trash2 } from "lucide-react";
import { StatusPill } from "@/components/status-pill";
import { RunContext } from "@/components/run-context";
import { Runline } from "@/components/runline";
import { InstructionBlock } from "@/components/instruction-block";
import { RunComposer, type ComposerResult } from "@/components/run-composer";
import { TerminalTail } from "@/components/terminal-tail";
import { ConfirmAction } from "@/components/confirm-action";
import { RenameAgentDialog } from "@/components/rename-dialog";
import { SendKeysSheet } from "@/components/send-keys-sheet";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useAppState } from "@/hooks/use-app-store";
import { useMutations } from "@/hooks/use-mutations";
import { useNow } from "@/hooks/use-now";
import { useRunAdapter, useRunList, useRunState } from "@/hooks/use-runs";
import { useRouteTitle } from "@/hooks/use-route-title";
import { useVisualViewport } from "@/hooks/use-visual-viewport";
import { classifySend } from "@/lib/run-adapter";
import { runStore } from "@/lib/run-store";
import { explainMissingRun, findRun, runRef, runStatusDescription, type Run, type RunKey } from "@/lib/run";
import { CONNECTION_MESSAGES } from "@/lib/connection";
import { checkPaneTarget } from "@/lib/pane-ops";

/** Sticky header: back, identity, textual status, overflow. */
function RunHeader({ run, onFocusAgent }: { run: Run; onFocusAgent: () => void }) {
  const navigate = useNavigate();
  const heading = useRouteTitle(run.agentName);

  return (
    <div className="border-b border-seam bg-deck px-2 pt-1">
      <div className="flex items-center gap-1">
        <Button variant="quiet" size="icon" aria-label="Back to agents" onClick={() => navigate("/")}>
          <ChevronLeft className="size-5" />
        </Button>
        <div className="min-w-0 flex-1">
          <h1 ref={heading} tabIndex={-1} className="truncate text-prose font-semibold text-mist">
            {run.agentName}
          </h1>
        </div>
        <StatusPill status={run.status} />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="quiet" size="icon" aria-label={`Actions for ${run.agentName}`}>
              <Ellipsis className="size-5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem asChild>
              <Link to={`/console/${encodeURIComponent(run.paneId)}?generation=${run.generation}`}>
                <SquareTerminal className="size-4" /> Open console
              </Link>
            </DropdownMenuItem>
            {/* Focusing the agent on the Mac is an explicit, separate act.
                Opening a run only reads remote state. */}
            <DropdownMenuItem onSelect={onFocusAgent}>
              <Crosshair className="size-4" /> Focus this agent on the Mac
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <RenameAgentDialog
              run={run}
              trigger={
                <DropdownMenuItem onSelect={(e) => e.preventDefault()}>
                  <Pencil className="size-4" /> Rename agent
                </DropdownMenuItem>
              }
            />
            <SendKeysSheet
              run={run}
              trigger={
                <DropdownMenuItem onSelect={(e) => e.preventDefault()}>
                  <Keyboard className="size-4" /> Send a key
                </DropdownMenuItem>
              }
            />
            <DropdownMenuSeparator />
            <ConfirmAction
              operation="pane.close"
              resourceId={run.paneId}
              label={run.agentName}
              params={{ pane_id: run.paneId }}
              expectedGeneration={run.generation}
              trigger={
                <DropdownMenuItem destructive onSelect={(e) => e.preventDefault()}>
                  <Trash2 className="size-4" /> Close this pane
                </DropdownMenuItem>
              }
            />
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <RunContext run={run} className="pb-1 pl-1" />
    </div>
  );
}

/**
 * A run whose pane generation or agent incarnation moved on. Frozen, never
 * silently rebound: the operator is shown what happened and offered the new
 * occupant as a deliberate choice.
 */
function InvalidRun({ identity }: { identity: RunKey | null }) {
  const { runs } = useRunList();
  const { snapshot } = useAppState();
  const heading = useRouteTitle("Run unavailable");
  const paneId = identity?.paneId;
  const generation = identity?.generation;
  const invalidation = useMemo(
    () => explainMissingRun(runs, snapshot, paneId ? { paneId, generation: generation ?? 0 } : null),
    [runs, snapshot, paneId, generation],
  );
  const key = identity;

  const message =
    invalidation?.kind === "replaced" ? CONNECTION_MESSAGES["pane-replaced"] : CONNECTION_MESSAGES["agent-ended"];

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-4 py-6">
      <h1 ref={heading} tabIndex={-1} className="text-prose font-semibold text-mist">
        {message.title}
      </h1>
      <p className="mt-1 max-w-prose text-body text-muted-ink">{message.detail}</p>
      <dl className="tabular mt-4 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
        <dt className="text-faint-ink">Pane</dt>
        <dd className="text-mist">{key?.paneId ?? "unknown"}</dd>
        <dt className="text-faint-ink">Generation you were on</dt>
        <dd className="text-mist">{key?.generation ?? "unknown"}</dd>
        {invalidation?.kind === "generation-changed" && (
          <>
            <dt className="text-faint-ink">Generation now</dt>
            <dd className="text-mist">{invalidation.generation}</dd>
          </>
        )}
      </dl>
      <div className="mt-5 flex flex-wrap gap-2">
        {invalidation?.kind === "replaced" && (
          <Button asChild variant="primary">
            <Link to={`/runs/${encodeURIComponent(invalidation.successor.id)}`}>Open the new occupant</Link>
          </Button>
        )}
        {key && (
          <Button asChild variant="outline">
            <Link to={`/console/${encodeURIComponent(key.paneId)}`}>
              <SquareTerminal className="size-4" /> Open console
            </Link>
          </Button>
        )}
        <Button asChild variant="quiet">
          <Link to="/">Back to agents</Link>
        </Button>
      </div>
    </div>
  );
}

export function RunRoute() {
  const { runId = "" } = useParams();
  const { runs, loading } = useRunList();
  const run = useMemo(() => findRun(runs, runId), [runs, runId]);

  // The relay's run id is opaque, so the execution identity a frozen run should
  // report comes from the last run this route actually resolved — never from
  // parsing the id. The fallback's internal id is parsed only when there is no
  // resolved run to remember.
  const [remembered, setRemembered] = useState<RunKey | null>(null);
  const paneId = run?.paneId;
  const generation = run?.generation;
  useEffect(() => {
    if (paneId && generation !== undefined) setRemembered({ paneId, generation });
  }, [paneId, generation]);
  const identity = run ? { paneId: run.paneId, generation: run.generation } : runRef(runId, null) ?? remembered;

  // A run the relay reported as invalid mid-read is frozen even while it is
  // still listed: its pane now belongs to a different incarnation.
  const [invalidated, setInvalidated] = useState<string | null>(null);
  const onInvalidated = useCallback(() => setInvalidated(run?.id ?? runId), [run?.id, runId]);

  if (!run) {
    // The structured list is still loading: say so rather than claiming the run
    // is gone.
    if (loading) return <RunLoading />;
    return <InvalidRun identity={identity} />;
  }
  if (invalidated === run.id) return <InvalidRun identity={identity} />;
  return <RunDetail key={run.id} run={run} onInvalidated={onInvalidated} />;
}

function RunLoading() {
  const heading = useRouteTitle("Loading run");
  return (
    <div className="flex min-h-0 flex-1 flex-col px-4 py-6">
      <h1 ref={heading} tabIndex={-1} className="text-prose font-semibold text-mist">
        Loading this run…
      </h1>
      <p className="mt-1 text-body text-muted-ink">Reading the run list from the relay.</p>
    </div>
  );
}

function RunDetail({ run, onInvalidated }: { run: Run; onInvalidated: () => void }) {
  const state = useRunState(run.id);
  const adapter = useRunAdapter();
  const { runPane, pending } = useMutations();
  const { connection } = useAppState();
  const vp = useVisualViewport();
  const now = useNow(15_000);

  const scroller = useRef<HTMLDivElement | null>(null);
  const [atBottom, setAtBottom] = useState(true);

  const generationProblem = checkPaneTarget(run);
  const offline = connection === "lost";

  const send = useCallback(
    async (text: string): Promise<ComposerResult> => {
      const body = text.trim();
      if (!body) return "kept";
      if (generationProblem) return "kept";
      const id = runStore.beginSend(run.id, body);
      let res = null;
      let thrown: unknown = undefined;
      try {
        res = await runPane("agent.prompt", run, { text: body });
      } catch (err) {
        thrown = err;
      }
      const outcome = classifySend(res, thrown);
      if (outcome.kind === "accepted") {
        runStore.settleSend(run.id, id, "accepted");
        return "accepted";
      }
      if (outcome.kind === "delivery_unknown") {
        // Tracked in the run with its own explicit recovery. Never retried here.
        runStore.settleSend(run.id, id, "delivery_unknown", outcome.message);
        return "uncertain";
      }
      runStore.settleSend(run.id, id, "rejected", outcome.message);
      return "kept";
    },
    [generationProblem, run, runPane],
  );

  // Follow the tail only while the operator is already at the bottom, so
  // reading history is never yanked away by an arriving entry.
  useEffect(() => {
    if (!atBottom) return;
    const node = scroller.current;
    if (node) node.scrollTop = node.scrollHeight;
  }, [atBottom, state.instructions, state.observed]);

  const onScroll = () => {
    const node = scroller.current;
    if (!node) return;
    setAtBottom(node.scrollHeight - node.scrollTop - node.clientHeight < 48);
  };

  const jumpToLatest = () => {
    const node = scroller.current;
    if (!node) return;
    node.scrollTo({ top: node.scrollHeight, behavior: "smooth" });
    setAtBottom(true);
  };

  const composerDisabled = !!generationProblem || offline;
  const composerReason = generationProblem
    ? generationProblem.message
    : offline
      ? "Can't reach your Mac. Your draft is kept here until the link is back."
      : undefined;

  return (
    <div className="flex min-h-0 flex-1 flex-col" style={{ paddingBottom: vp.keyboardInset }}>
      <RunHeader run={run} onFocusAgent={() => void runPane("agent.focus", run)} />

      <div className="relative min-h-0 flex-1">
        <div
          ref={scroller}
          onScroll={onScroll}
          role="log"
          aria-live="polite"
          aria-relevant="additions"
          aria-label={`Activity for ${run.agentName}`}
          className="h-full overflow-y-auto px-4 py-4"
        >
          {/* A bounded reading column: agent prose is a document, and a
              1400px-wide line of body copy is not readable. */}
          <div className="mx-auto flex w-full max-w-[46rem] flex-col gap-4">
            <section>
              {/* The status word is already in the header; this heading is the
                document's structure, not a second copy of the badge. */}
              <h2 className="text-body font-semibold text-mist">Status</h2>
              <p className="mt-1 max-w-prose text-prose text-muted-ink">{runStatusDescription(run.status)}</p>
              {run.terminalTitle && (
                <p className="mt-2 max-w-prose text-body text-mist">
                  <span className="text-faint-ink">Pane title: </span>
                  {run.terminalTitle}
                </p>
              )}
            </section>

            {state.instructions.length > 0 && (
              <section aria-labelledby="instructions-heading" className="flex flex-col gap-2">
                <h2 id="instructions-heading" className="text-body font-semibold text-mist">
                  Your instructions
                </h2>
                {state.instructions.map((instruction) => (
                  <InstructionBlock
                    key={instruction.id}
                    instruction={instruction}
                    now={now}
                    onResend={(text) => void send(text)}
                    onDismiss={(id) => runStore.dismiss(run.id, id)}
                  />
                ))}
              </section>
            )}

            <section aria-labelledby="observed-heading">
              <h2 id="observed-heading" className="text-body font-semibold text-mist">
                Observed activity
              </h2>
              <p className="mb-2 mt-1 max-w-prose text-meta text-muted-ink">
                {adapter.supportsMessages
                  ? "Reported by the relay."
                  : "Herdr publishes no agent messages, so this is the status changes this phone saw while it was connected."}
              </p>
              {state.observed.length > 0 ? (
                <Runline events={state.observed} now={now} />
              ) : (
                <p className="text-body text-muted-ink">Nothing observed yet on this device.</p>
              )}
            </section>

            <TerminalTail run={run} now={now} onInvalidated={onInvalidated} />
          </div>
        </div>

        {!atBottom && (
          <Button
            variant="default"
            size="sm"
            className="absolute bottom-3 left-1/2 -translate-x-1/2 shadow-lg"
            onClick={jumpToLatest}
          >
            <ArrowDown className="size-4" /> Jump to latest
          </Button>
        )}
      </div>

      <RunComposer
        run={run}
        value={state.draft}
        onChange={(next) => runStore.setDraft(run.id, next)}
        onSubmit={send}
        pending={pending}
        disabled={composerDisabled}
        disabledReason={composerReason}
      />
    </div>
  );
}
