import { useCallback, useSyncExternalStore } from "react";
import { store } from "@/lib/store";
import { useMutations } from "@/hooks/use-mutations";
import {
  draftProblem,
  launchStore,
  nextStep,
  type LaunchDraft,
  type LaunchState,
} from "@/lib/launch";
import { formatRunId } from "@/lib/run";
import { classifySend } from "@/lib/run-adapter";
import type { MutationResponse, Snapshot } from "@/lib/types";

/** Read a nested `{ <key>: { <id field>: string } }` out of a mutation result. */
function idFrom(res: MutationResponse | null, container: string, field: string): string | undefined {
  if (!res || "error" in res) return undefined;
  const result = (res as { result?: unknown }).result;
  if (!result || typeof result !== "object") return undefined;
  const nested = (result as Record<string, unknown>)[container];
  if (!nested || typeof nested !== "object") return undefined;
  const value = (nested as Record<string, unknown>)[field];
  return typeof value === "string" && value ? value : undefined;
}

/**
 * Collapse a mutation result to a message, for the steps where a plain failure
 * is the right reading.
 *
 * The **prompt** step deliberately does not use this — see `classifySend` below.
 * Retrying a creation step can at worst leave a second visible, removable
 * workspace or a refused duplicate agent name; retrying an instruction can put
 * the same text into a live shell twice, which nothing undoes.
 */
function errorOf(res: MutationResponse | null): string | null {
  if (!res) return "The relay did not answer.";
  if ("error" in res && res.error) return res.error.message;
  return null;
}

/**
 * Wait for the snapshot to carry a pane and its lifecycle generation.
 *
 * A freshly created pane has no generation until the next snapshot lands, and
 * `agent.start` is generation-guarded, so the orchestration has to observe the
 * pane rather than guess. Poll-as-truth still applies: this nudges a revalidate
 * and waits for the store, it never fabricates a generation.
 */
function awaitPaneGeneration(paneId: string, timeoutMs = 12_000): Promise<number> {
  const read = () => {
    const snapshot: Snapshot | null = store.getState().snapshot;
    return snapshot?.panes.find((p) => p.id === paneId)?.generation ?? 0;
  };
  const immediate = read();
  if (immediate > 0) return Promise.resolve(immediate);

  return new Promise((resolve) => {
    let settled = false;
    const finish = (value: number) => {
      if (settled) return;
      settled = true;
      clearInterval(poll);
      clearTimeout(deadline);
      unsubscribe();
      resolve(value);
    };
    const unsubscribe = store.subscribe(() => {
      const gen = read();
      if (gen > 0) finish(gen);
    });
    const poll = setInterval(() => store.revalidate(), 900);
    const deadline = setTimeout(() => finish(read()), timeoutMs);
    store.revalidate();
  });
}

export function useLaunchState(): LaunchState {
  return useSyncExternalStore(launchStore.subscribe, launchStore.getState, launchStore.getState);
}

export interface LaunchController {
  state: LaunchState;
  patchDraft: (patch: Partial<LaunchDraft>) => void;
  /** Run every outstanding step in order, preserving completed work. */
  launch: () => Promise<void>;
  /** Retry from the first failed step; nothing already done is repeated. */
  retry: () => Promise<void>;
  /**
   * Deliberately re-send the objective after an uncertain delivery. Separate
   * from `retry` on purpose: it may duplicate an instruction, so it is never
   * reached by the generic failed-step path.
   */
  resendObjective: () => Promise<void>;
  reset: () => void;
  problem: string | null;
}

/**
 * Drive the Start run orchestration.
 *
 * Each step is a distinct server operation with its own recorded outcome. A
 * failure stops the sequence and leaves everything already created in place —
 * the launch receipt names it, and a retry resumes from the broken step. No
 * successfully created workspace, worktree, or pane is ever deleted to make the
 * sequence look transactional.
 */
export function useLaunch(): LaunchController {
  const state = useLaunchState();
  const { run, runPane } = useMutations();

  const execute = useCallback(async () => {
    launchStore.setPhase("running");

    for (let guard = 0; guard < 8; guard++) {
      const step = nextStep(launchStore.getState().steps);
      if (!step) break;
      const { draft, created } = launchStore.getState();
      launchStore.setStep(step.id, { status: "running", error: null });

      const fail = (message: string) => {
        launchStore.setStep(step.id, { status: "failed", error: message });
        launchStore.setPhase("settled");
      };

      if (step.id === "workspace") {
        if (draft.targetKind === "existing") {
          const snapshot = store.getState().snapshot;
          const workspace = snapshot?.workspaces.find((w) => w.id === draft.workspaceId);
          if (!workspace) {
            fail("That workspace is no longer open in Herdr.");
            return;
          }
          launchStore.recordCreated({ workspaceId: workspace.id });
          launchStore.setStep(step.id, { status: "done", detail: `Using ${workspace.label}` });
          continue;
        }
        if (draft.targetKind === "new-workspace") {
          const res = await run("workspace.create", {
            label: draft.workspaceLabel.trim(),
            cwd: draft.cwd,
            focus: false,
          });
          const error = errorOf(res);
          if (error) {
            fail(error);
            return;
          }
          launchStore.recordCreated({
            workspaceId: idFrom(res, "workspace", "workspace_id"),
            tabId: idFrom(res, "tab", "tab_id"),
            paneId: idFrom(res, "root_pane", "pane_id"),
          });
          launchStore.setStep(step.id, { status: "done", detail: `Created workspace ${draft.workspaceLabel.trim()}` });
          continue;
        }
        // new-worktree: Herdr creates the worktree AND opens it as a workspace
        // with its first tab and root pane, so this is one operation, not two.
        const res = await run("worktree.create", {
          cwd: draft.cwd,
          branch: draft.branch.trim(),
          base: draft.base.trim() || undefined,
          label: draft.branch.trim(),
          focus: false,
        });
        const error = errorOf(res);
        if (error) {
          fail(error);
          return;
        }
        launchStore.recordCreated({
          worktreePath: idFrom(res, "worktree", "path"),
          workspaceId: idFrom(res, "workspace", "workspace_id"),
          tabId: idFrom(res, "tab", "tab_id"),
          paneId: idFrom(res, "root_pane", "pane_id"),
        });
        launchStore.setStep(step.id, { status: "done", detail: `Created worktree ${draft.branch.trim()}` });
        continue;
      }

      if (step.id === "pane") {
        let paneId = created.paneId ?? null;
        if (!paneId) {
          const snapshot = store.getState().snapshot;
          const workspaceId = created.workspaceId;
          const panes = (snapshot?.panes ?? []).filter((p) => p.workspaceId === workspaceId);
          const free = panes.find((p) => !p.agentKind);
          if (free) {
            paneId = free.id;
          } else if (panes.length > 0) {
            // Every pane is occupied: split one rather than displace an agent.
            const host = panes[0];
            const res = await runPane("pane.split", { paneId: host.id, generation: host.generation }, {
              direction: "right",
              focus: false,
            });
            const error = errorOf(res);
            if (error) {
              fail(error);
              return;
            }
            paneId = idFrom(res, "pane", "pane_id") ?? null;
          }
        }
        if (!paneId) {
          fail("No shell pane is available in that workspace.");
          return;
        }
        const generation = await awaitPaneGeneration(paneId);
        if (generation <= 0) {
          fail("Herdr has not reported a lifecycle generation for the new pane yet. Retry in a moment.");
          launchStore.recordCreated({ paneId });
          return;
        }
        launchStore.recordCreated({ paneId });
        launchStore.setStep(step.id, { status: "done", detail: `Pane ${paneId} (generation ${generation})` });
        continue;
      }

      if (step.id === "agent") {
        const paneId = launchStore.getState().created.paneId;
        const generation = paneId ? await awaitPaneGeneration(paneId) : 0;
        if (!paneId || generation <= 0) {
          fail("The target pane is no longer available.");
          return;
        }
        const res = await runPane("agent.start", { paneId, generation }, {
          kind: draft.agentKind,
          name: draft.agentName.trim(),
        });
        const error = errorOf(res);
        if (error) {
          fail(error);
          return;
        }
        launchStore.recordCreated({ agentName: draft.agentName.trim(), runId: formatRunId({ paneId, generation }) });
        launchStore.setStep(step.id, { status: "done", detail: `Started ${draft.agentKind} as ${draft.agentName.trim()}` });
        continue;
      }

      // prompt
      const paneId = launchStore.getState().created.paneId;
      const generation = paneId ? await awaitPaneGeneration(paneId) : 0;
      if (!paneId || generation <= 0) {
        fail("The target pane is no longer available.");
        return;
      }
      launchStore.recordCreated({ runId: formatRunId({ paneId, generation }) });
      const res = await runPane("agent.prompt", { paneId, generation }, { text: draft.objective.trim() });
      // The same classifier the composer uses. A retryable failure means the
      // relay lost certainty *after* Herdr may have accepted the text, so it is
      // recorded as an uncertain delivery — never as a plain failure that the
      // generic retry would silently re-send.
      const outcome = classifySend(res);
      if (outcome.kind === "delivery_unknown") {
        launchStore.setStep(step.id, {
          status: "delivery_unknown",
          detail: "The relay lost certainty before the objective was confirmed",
          error: outcome.message,
        });
        launchStore.setPhase("settled");
        return;
      }
      if (outcome.kind === "rejected") {
        // The agent is running and the workspace exists; only the first
        // instruction was refused. Say exactly that instead of unwinding the run.
        fail(outcome.message);
        return;
      }
      launchStore.setStep(step.id, { status: "done", detail: "Objective delivered" });
    }

    launchStore.setPhase("settled");
  }, [run, runPane]);

  /**
   * Send the objective again after an uncertain delivery.
   *
   * Never reached automatically: the receipt exposes it as a separate, warned
   * action, so a duplicate instruction to a live shell is always a decision the
   * operator made with the console one tap away.
   */
  const resendObjective = useCallback(async () => {
    const { created, draft } = launchStore.getState();
    const paneId = created.paneId;
    if (!paneId) return;
    launchStore.setStep("prompt", { status: "running", error: null, detail: null });
    const generation = await awaitPaneGeneration(paneId);
    if (generation <= 0) {
      launchStore.setStep("prompt", { status: "failed", error: "The target pane is no longer available." });
      return;
    }
    const res = await runPane("agent.prompt", { paneId, generation }, { text: draft.objective.trim() });
    const outcome = classifySend(res);
    if (outcome.kind === "accepted") {
      launchStore.setStep("prompt", { status: "done", detail: "Objective delivered", error: null });
      return;
    }
    if (outcome.kind === "delivery_unknown") {
      launchStore.setStep("prompt", {
        status: "delivery_unknown",
        detail: "The relay lost certainty before the objective was confirmed",
        error: outcome.message,
      });
      return;
    }
    launchStore.setStep("prompt", { status: "failed", error: outcome.message });
  }, [runPane]);

  const launch = useCallback(async () => {
    if (draftProblem(launchStore.getState().draft)) return;
    await execute();
  }, [execute]);

  const retry = useCallback(async () => {
    launchStore.prepareRetry();
    await execute();
  }, [execute]);

  return {
    state,
    patchDraft: (patch) => launchStore.patchDraft(patch),
    launch,
    retry,
    resendObjective,
    reset: () => launchStore.reset(),
    problem: draftProblem(state.draft),
  };
}
