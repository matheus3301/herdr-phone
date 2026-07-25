import { useEffect, useMemo } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Check, CircleAlert, CircleDashed, CircleHelp, Loader, MinusCircle, Rocket } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { DirectoryPicker } from "@/components/directory-picker";
import { useAppState } from "@/hooks/use-app-store";
import { useLaunch } from "@/hooks/use-launch";
import { useRouteTitle } from "@/hooks/use-route-title";
import { isValidAgentName, suggestAgentName } from "@/lib/agent-name";
import {
  launchDeliveryUnknown,
  launchHasRetryableStep,
  launchPartiallySucceeded,
  launchSucceeded,
  type LaunchStep,
  type LaunchTargetKind,
} from "@/lib/launch";
import { cn } from "@/lib/utils";

const STEP_ICON = {
  pending: CircleDashed,
  running: Loader,
  done: Check,
  failed: CircleAlert,
  delivery_unknown: CircleHelp,
  skipped: MinusCircle,
} as const;

const STEP_TONE = {
  pending: "text-faint-ink",
  running: "text-tide",
  done: "text-tide",
  failed: "text-flare",
  // Brass, not flare: an uncertain delivery is not a refusal, and colouring it
  // as one pushes the operator toward re-sending without thinking.
  delivery_unknown: "text-brass",
  skipped: "text-faint-ink",
} as const;

/** The word a step's outcome is announced with, for the screen reader. */
const STEP_STATUS_TEXT = {
  pending: "pending",
  running: "running",
  done: "done",
  failed: "failed",
  delivery_unknown: "delivery unknown",
  skipped: "skipped",
} as const;

function Receipt({ steps }: { steps: LaunchStep[] }) {
  return (
    <ol className="runline" aria-label="Launch steps">
      {steps.map((step) => {
        const Icon = STEP_ICON[step.status];
        return (
          <li
            key={step.id}
            data-tone={step.status === "failed" ? "attention" : step.status === "done" ? "settled" : undefined}
            className="py-1.5"
          >
            <p className={cn("flex items-center gap-1.5 text-body", STEP_TONE[step.status])}>
              <Icon className="size-4 shrink-0" aria-hidden />
              <span className="text-mist">{step.title}</span>
              <span className="sr-only">{STEP_STATUS_TEXT[step.status]}</span>
            </p>
            {step.detail && <p className="tabular text-faint-ink">{step.detail}</p>}
            {step.error && (
              <p
                className={cn("text-meta", step.status === "failed" ? "text-flare" : "text-muted-ink")}
                role={step.status === "failed" ? "alert" : undefined}
              >
                {step.error}
              </p>
            )}
          </li>
        );
      })}
    </ol>
  );
}

function ChoiceCard({
  selected,
  onSelect,
  title,
  description,
  disabled,
  disabledReason,
}: {
  selected: boolean;
  onSelect: () => void;
  title: string;
  description: string;
  disabled?: boolean;
  disabledReason?: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      disabled={disabled}
      onClick={onSelect}
      className={cn(
        "flex min-h-11 w-full flex-col items-start gap-0.5 rounded-log px-3 py-2.5 text-left",
        "focus-visible:outline-2 focus-visible:outline-brass focus-visible:outline-offset-2",
        "disabled:cursor-not-allowed disabled:opacity-50",
        selected ? "bg-brass/12 ring-1 ring-brass" : "bg-hull ring-1 ring-seam hover:bg-bulkhead",
      )}
    >
      <span className="text-body font-medium text-mist">{title}</span>
      <span className="text-meta text-muted-ink">{disabled && disabledReason ? disabledReason : description}</span>
    </button>
  );
}

export function StartRunRoute() {
  const heading = useRouteTitle("Start run");
  const navigate = useNavigate();
  const { snapshot, capabilities } = useAppState();
  const { state, patchDraft, launch, retry, resendObjective, reset, problem } = useLaunch();
  const { draft, steps, phase, created } = state;

  const operations = useMemo(() => new Set(capabilities?.operations ?? []), [capabilities]);
  const kinds = capabilities?.agentKinds ?? [];
  const kindsAvailable = capabilities?.agentKindsAvailable ?? false;
  const canCreateWorkspace = operations.has("workspace.create");
  const canCreateWorktree = operations.has("worktree.create");
  const canStartAgent = operations.has("agent.start");
  const workspaces = snapshot?.workspaces ?? [];
  const existingNames = useMemo(() => (snapshot?.agents ?? []).map((a) => a.name), [snapshot]);

  // Suggest a unique, backend-valid name from the chosen kind until edited.
  useEffect(() => {
    if (draft.agentKind && !draft.agentNameEdited) {
      patchDraft({
        agentName: suggestAgentName(draft.agentKind, existingNames),
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft.agentKind, draft.agentNameEdited, existingNames.join(",")]);

  // Default the workspace to the focused one so the common case is one tap.
  useEffect(() => {
    if (draft.targetKind === "existing" && !draft.workspaceId && workspaces.length > 0) {
      patchDraft({
        workspaceId: snapshot?.focusedWorkspaceId ?? workspaces[0].id,
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft.targetKind, draft.workspaceId, workspaces.length]);

  const nameTaken = existingNames.includes(draft.agentName.trim());
  const nameMalformed = !!draft.agentName && !isValidAgentName(draft.agentName.trim());
  const blocked =
    problem ??
    (nameTaken ? "That agent name is already in use." : nameMalformed ? "That agent name is not valid." : null);

  if (!canStartAgent || !kindsAvailable) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-6">
        <h1 ref={heading} tabIndex={-1} className="text-prose font-semibold text-mist">
          Start run
        </h1>
        <p className="mt-2 max-w-prose text-body text-muted-ink">
          {canStartAgent
            ? "The relay discovered no agent kinds on your Mac, so no agent can be started from here."
            : "This relay does not allow starting agents."}{" "}
          You can still open a workspace and drive a pane through the console.
        </p>
        <Button asChild variant="outline" className="mt-4">
          <Link to="/workspaces">Go to workspaces</Link>
        </Button>
      </div>
    );
  }

  if (phase !== "compose") {
    const succeeded = launchSucceeded(steps);
    const partial = launchPartiallySucceeded(steps);
    const uncertain = launchDeliveryUnknown(steps);
    const retryable = launchHasRetryableStep(steps);
    return (
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-5">
        <h1 ref={heading} tabIndex={-1} className="text-prose font-semibold text-mist">
          {phase === "running"
            ? "Starting the run…"
            : succeeded
              ? "Run started"
              : uncertain
                ? "Agent started, delivery unknown"
                : "Run partly started"}
        </h1>
        <p className="mt-1 max-w-prose text-body text-muted-ink">
          {succeeded
            ? "Every step completed. The run is now in your inbox."
            : uncertain
              ? "The agent is running and is in your inbox. The relay could not confirm the objective reached it, and it may already have. Check the console before sending it again."
              : partial
                ? "Some steps completed and were kept. Nothing that succeeded has been undone — retry the failed step or finish it yourself."
                : "The launch stopped at the first failed step."}
        </p>

        <div className="mt-4">
          <Receipt steps={steps} />
        </div>

        {(created.workspaceId || created.paneId || created.worktreePath) && (
          <dl className="tabular mt-4 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
            {created.worktreePath && (
              <>
                <dt className="text-faint-ink">Worktree</dt>
                <dd className="truncate text-mist">{created.worktreePath}</dd>
              </>
            )}
            {created.workspaceId && (
              <>
                <dt className="text-faint-ink">Workspace</dt>
                <dd className="text-mist">{created.workspaceId}</dd>
              </>
            )}
            {created.paneId && (
              <>
                <dt className="text-faint-ink">Pane</dt>
                <dd className="text-mist">{created.paneId}</dd>
              </>
            )}
          </dl>
        )}

        <div className="mt-5 flex flex-wrap gap-2">
          {/* The run id is recorded at the *agent* step, so it exists whenever the
              agent is live — including after a refused or uncertain first
              instruction. Withholding the route to a run that is already in the
              inbox is weaker recovery than the launch receipt owes the operator. */}
          {created.runId && (
            <Button
              variant={succeeded ? "primary" : "outline"}
              onClick={() => {
                const id = created.runId!;
                reset();
                navigate(`/runs/${encodeURIComponent(id)}`);
              }}
            >
              Open the run
            </Button>
          )}
          {retryable && phase === "settled" && (
            <Button variant="primary" onClick={() => void retry()}>
              Retry the failed step
            </Button>
          )}
          {/* Deliberate, separately labelled, and warned — never folded into
              "Retry the failed step", which would re-send an instruction the
              agent may already have. */}
          {uncertain && phase === "settled" && (
            <Button variant="outline" onClick={() => void resendObjective()}>
              Send the objective again
            </Button>
          )}
          {created.workspaceId && (
            <Button asChild variant="outline">
              <Link to={`/workspaces/${encodeURIComponent(created.workspaceId)}`}>Inspect the workspace</Link>
            </Button>
          )}
          {created.paneId && (
            <Button asChild variant="outline">
              <Link to={`/console/${encodeURIComponent(created.paneId)}`}>Open console</Link>
            </Button>
          )}
          <Button variant="quiet" onClick={() => reset()} disabled={phase === "running"}>
            Start another run
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-5">
        <div className="mx-auto w-full max-w-[46rem]">
          <h1 ref={heading} tabIndex={-1} className="text-prose font-semibold text-mist">
            Start run
          </h1>
          <p className="mt-1 max-w-prose text-body text-muted-ink">
            Four separate operations on your Mac. Each one reports its own result, and anything created is kept even if
            a later step fails.
          </p>

          <section className="mt-5">
            <Label htmlFor="objective">What should the agent do?</Label>
            <Textarea
              id="objective"
              className="mt-1.5"
              maxHeight={220}
              value={draft.objective}
              placeholder="Make reconnect preserve the active session."
              onChange={(e) => patchDraft({ objective: e.target.value })}
            />
          </section>

          <section className="mt-5" role="radiogroup" aria-label="Where should it run?">
            <h2 className="text-body font-semibold text-mist">Where should it run?</h2>
            <div className="mt-2 flex flex-col gap-2">
              <ChoiceCard
                selected={draft.targetKind === "existing"}
                onSelect={() => patchDraft({ targetKind: "existing" as LaunchTargetKind })}
                title="An existing workspace"
                description="Reuse a workspace that is already open."
                disabled={workspaces.length === 0}
                disabledReason="No workspaces are open."
              />
              <ChoiceCard
                selected={draft.targetKind === "new-workspace"}
                onSelect={() => patchDraft({ targetKind: "new-workspace" })}
                title="A new workspace"
                description="Create a workspace in a directory you choose."
                disabled={!canCreateWorkspace}
                disabledReason="This relay does not allow creating workspaces."
              />
              <ChoiceCard
                selected={draft.targetKind === "new-worktree"}
                onSelect={() => patchDraft({ targetKind: "new-worktree" })}
                title="A new git worktree"
                description="Branch off an existing repository; Herdr opens it as a workspace."
                disabled={!canCreateWorktree}
                disabledReason="This relay does not allow creating worktrees."
              />
            </div>

            {draft.targetKind === "existing" && workspaces.length > 0 && (
              <div className="mt-3 flex flex-col gap-2">
                <Label htmlFor="workspace-select">Workspace</Label>
                <select
                  id="workspace-select"
                  value={draft.workspaceId ?? ""}
                  onChange={(e) => patchDraft({ workspaceId: e.target.value })}
                  className="h-11 w-full rounded-log bg-hull px-3 text-body text-mist ring-1 ring-seam focus-visible:outline-2 focus-visible:outline-brass"
                >
                  {workspaces.map((w) => (
                    <option key={w.id} value={w.id}>
                      {w.label}
                      {w.worktree && w.worktree.repoName !== w.label ? ` / ${w.worktree.repoName}` : ""}
                    </option>
                  ))}
                </select>
              </div>
            )}

            {draft.targetKind === "new-workspace" && (
              <div className="mt-3 flex flex-col gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="ws-label">Workspace name</Label>
                  <Input
                    id="ws-label"
                    value={draft.workspaceLabel}
                    onChange={(e) => patchDraft({ workspaceLabel: e.target.value })}
                    placeholder="space-api"
                    autoComplete="off"
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label>Working directory</Label>
                  <DirectoryPicker value={draft.cwd} onChange={(cwd) => patchDraft({ cwd })} />
                </div>
              </div>
            )}

            {draft.targetKind === "new-worktree" && (
              <div className="mt-3 flex flex-col gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="wt-branch">New branch</Label>
                  <Input
                    id="wt-branch"
                    value={draft.branch}
                    onChange={(e) => patchDraft({ branch: e.target.value })}
                    placeholder="feature/reconnect"
                    autoComplete="off"
                    autoCapitalize="off"
                    spellCheck={false}
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="wt-base">Base branch</Label>
                  <Input
                    id="wt-base"
                    value={draft.base}
                    onChange={(e) => patchDraft({ base: e.target.value })}
                    autoComplete="off"
                    autoCapitalize="off"
                    spellCheck={false}
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label>Repository</Label>
                  <DirectoryPicker value={draft.cwd} onChange={(cwd) => patchDraft({ cwd })} />
                </div>
              </div>
            )}
          </section>

          <section className="mt-5">
            <h2 className="text-body font-semibold text-mist">Which agent?</h2>
            <div className="mt-2 flex flex-wrap gap-2" role="radiogroup" aria-label="Agent kind">
              {kinds.map((kind) => (
                <button
                  key={kind}
                  type="button"
                  role="radio"
                  aria-checked={draft.agentKind === kind}
                  onClick={() => patchDraft({ agentKind: kind })}
                  className={cn(
                    "min-h-11 rounded-log px-3.5 text-body focus-visible:outline-2 focus-visible:outline-brass",
                    draft.agentKind === kind
                      ? "bg-brass/12 text-mist ring-1 ring-brass"
                      : "bg-hull text-muted-ink ring-1 ring-seam hover:text-mist",
                  )}
                >
                  {kind}
                </button>
              ))}
            </div>
            <div className="mt-3 flex flex-col gap-1.5">
              <Label htmlFor="agent-name">Name it</Label>
              <Input
                id="agent-name"
                value={draft.agentName}
                onChange={(e) =>
                  patchDraft({
                    agentName: e.target.value,
                    agentNameEdited: true,
                  })
                }
                placeholder="claude"
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                aria-invalid={nameTaken || nameMalformed}
              />
              <p className="text-meta text-muted-ink">
                Herdr identifies agents by name. Lowercase letters, digits, - or _, starting with a letter.
              </p>
            </div>
          </section>
        </div>
      </div>

      {/* The launch action sits outside the scroll owner rather than sticking
          inside it, so it can never overlay the form it belongs to. */}
      <div className="border-t border-seam bg-bulkhead px-4 pb-[calc(12px+var(--spacing-safe-bottom))] pt-3">
        <div className="mx-auto w-full max-w-[46rem]">
          {blocked && (
            <p className="mb-2 text-meta text-muted-ink" role="status">
              {blocked}
            </p>
          )}
          <Button variant="primary" className="w-full" disabled={!!blocked} onClick={() => void launch()}>
            <Rocket className="size-4" /> Start run
          </Button>
        </div>
      </div>
    </div>
  );
}
