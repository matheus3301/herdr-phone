/**
 * Start run — a resumable orchestration over the existing typed mutations.
 *
 * Creating a workspace, opening a worktree, starting an agent, and delivering a
 * first instruction are four independent server operations. Herdr offers no
 * transaction across them, so this state machine is explicit about that: each
 * step records its own outcome, a successful step is never undone because a
 * later one failed, and a failed run can be retried from the step that broke
 * without repeating the work already done.
 *
 * The draft lives in memory for the life of the tab, which is what makes
 * `/runs/new` a real route: dismissing it and coming back resumes exactly where
 * the operator left off. It is never persisted — the objective is user content
 * bound for a shell.
 */

export type LaunchTargetKind = "existing" | "new-workspace" | "new-worktree";

export const LAUNCH_STEPS = ["workspace", "pane", "agent", "prompt"] as const;
export type LaunchStepId = (typeof LAUNCH_STEPS)[number];

/**
 * `delivery_unknown` is a terminal outcome distinct from `failed`.
 *
 * The relay can lose certainty *after* Herdr may already have accepted a prompt.
 * Calling that "failed" and then offering the generic retry is how a launch
 * duplicates an instruction into a live shell, which the delivery contract
 * forbids: a timed-out message must never be silently retried. So this status
 * is excluded from `nextStep` and from `prepareRetry`, and the only way to send
 * again is a separately labelled, explicitly warned action — the same shape the
 * run composer already uses.
 */
export type LaunchStepStatus = "pending" | "running" | "done" | "failed" | "delivery_unknown" | "skipped";

export interface LaunchStep {
  id: LaunchStepId;
  title: string;
  status: LaunchStepStatus;
  /** What actually happened, in the operator's terms. */
  detail: string | null;
  error: string | null;
}

export const STEP_TITLE: Record<LaunchStepId, string> = {
  workspace: "Prepare the workspace",
  pane: "Locate a shell pane",
  agent: "Start the agent",
  prompt: "Send the objective",
};

export interface LaunchDraft {
  objective: string;
  targetKind: LaunchTargetKind;
  /** For `existing`: the workspace to run in. */
  workspaceId: string | null;
  /** For `new-workspace`: its label. */
  workspaceLabel: string;
  /** Working directory for a new workspace, or the repository for a worktree. */
  cwd: string;
  /** For `new-worktree`: the branch to create and its base. */
  branch: string;
  base: string;
  agentKind: string | null;
  agentName: string;
  agentNameEdited: boolean;
}

export interface LaunchCreated {
  workspaceId?: string;
  tabId?: string;
  paneId?: string;
  worktreePath?: string;
  agentName?: string;
  runId?: string;
}

export type LaunchPhase = "compose" | "running" | "settled";

export interface LaunchState {
  draft: LaunchDraft;
  phase: LaunchPhase;
  steps: LaunchStep[];
  created: LaunchCreated;
}

export const DEFAULT_CWD = "/Users/dev/code";

export function emptyDraft(): LaunchDraft {
  return {
    objective: "",
    targetKind: "existing",
    workspaceId: null,
    workspaceLabel: "",
    cwd: DEFAULT_CWD,
    branch: "",
    base: "main",
    agentKind: null,
    agentName: "",
    agentNameEdited: false,
  };
}

export function initialSteps(): LaunchStep[] {
  return LAUNCH_STEPS.map((id) => ({ id, title: STEP_TITLE[id], status: "pending", detail: null, error: null }));
}

function initialState(): LaunchState {
  return { draft: emptyDraft(), phase: "compose", steps: initialSteps(), created: {} };
}

/** Whether the composed draft can be launched. Pure, so the form can test it. */
export function draftProblem(draft: LaunchDraft): string | null {
  if (!draft.objective.trim()) return "Describe what the agent should do.";
  if (draft.targetKind === "existing" && !draft.workspaceId) return "Choose a workspace to run in.";
  if (draft.targetKind === "new-workspace" && !draft.workspaceLabel.trim()) return "Name the new workspace.";
  if (draft.targetKind === "new-worktree" && !draft.branch.trim()) return "Name the branch for the new worktree.";
  if (!draft.agentKind) return "Choose an agent to run.";
  if (!draft.agentName.trim()) return "Give the agent a name.";
  return null;
}

/** Statuses the orchestration must never run again on its own. */
const SETTLED: LaunchStepStatus[] = ["done", "skipped", "delivery_unknown"];

/**
 * The first step that still needs to run, or null when nothing may run.
 *
 * `delivery_unknown` counts as settled: re-running it is exactly the duplicate
 * send the delivery contract forbids.
 */
export function nextStep(steps: LaunchStep[]): LaunchStep | null {
  return steps.find((s) => !SETTLED.includes(s.status)) ?? null;
}

export function launchSucceeded(steps: LaunchStep[]): boolean {
  return steps.every((s) => s.status === "done" || s.status === "skipped");
}

/** True when the launch stopped with an uncertain — not refused — delivery. */
export function launchDeliveryUnknown(steps: LaunchStep[]): boolean {
  return steps.some((s) => s.status === "delivery_unknown");
}

export function launchPartiallySucceeded(steps: LaunchStep[]): boolean {
  const stopped = steps.some((s) => s.status === "failed" || s.status === "delivery_unknown");
  return stopped && steps.some((s) => s.status === "done");
}

/** True when a step the generic retry can safely repeat is outstanding. */
export function launchHasRetryableStep(steps: LaunchStep[]): boolean {
  return steps.some((s) => s.status === "failed");
}

/**
 * Module-level store so the launch survives navigating away from `/runs/new`.
 * One launch at a time: the phone drives one Herdr session, and a second
 * concurrent orchestration would make partial-success recovery ambiguous.
 */
export class LaunchStore {
  private state: LaunchState = initialState();
  private listeners = new Set<() => void>();

  subscribe = (cb: () => void): (() => void) => {
    this.listeners.add(cb);
    return () => this.listeners.delete(cb);
  };

  getState = (): LaunchState => this.state;

  private set(patch: Partial<LaunchState>) {
    this.state = { ...this.state, ...patch };
    for (const cb of this.listeners) cb();
  }

  patchDraft(patch: Partial<LaunchDraft>): void {
    this.set({ draft: { ...this.state.draft, ...patch } });
  }

  setPhase(phase: LaunchPhase): void {
    this.set({ phase });
  }

  /** Mark a step, preserving every other step's recorded outcome. */
  setStep(id: LaunchStepId, patch: Partial<Omit<LaunchStep, "id" | "title">>): void {
    this.set({ steps: this.state.steps.map((s) => (s.id === id ? { ...s, ...patch } : s)) });
  }

  /** Record a created resource. Creations are additive and never rolled back. */
  recordCreated(patch: LaunchCreated): void {
    this.set({ created: { ...this.state.created, ...patch } });
  }

  /**
   * Clear only the failure on the step being retried; keep completed work.
   *
   * Deliberately matches `failed` alone. A `delivery_unknown` step must never be
   * reset here: this is the generic "retry the failed step" path, and sweeping an
   * uncertain delivery back into it would re-send an instruction that may already
   * have reached the shell.
   */
  prepareRetry(): void {
    this.set({
      phase: "running",
      steps: this.state.steps.map((s) => (s.status === "failed" ? { ...s, status: "pending", error: null } : s)),
    });
  }

  reset(): void {
    this.state = initialState();
    for (const cb of this.listeners) cb();
  }
}

export const launchStore = new LaunchStore();
