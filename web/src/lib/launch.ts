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

export type LaunchStepStatus = "pending" | "running" | "done" | "failed" | "skipped";

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

/** The first step that still needs to run, or null when the launch is complete. */
export function nextStep(steps: LaunchStep[]): LaunchStep | null {
  return steps.find((s) => s.status !== "done" && s.status !== "skipped") ?? null;
}

export function launchSucceeded(steps: LaunchStep[]): boolean {
  return steps.every((s) => s.status === "done" || s.status === "skipped");
}

export function launchPartiallySucceeded(steps: LaunchStep[]): boolean {
  return steps.some((s) => s.status === "failed") && steps.some((s) => s.status === "done");
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

  /** Clear only the failure on the step being retried; keep completed work. */
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
