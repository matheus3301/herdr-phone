import { useMemo, useSyncExternalStore } from "react";
import { useAppState } from "@/hooks/use-app-store";
import { buildRuns, findRun, groupRuns, type Run, type RunGroup } from "@/lib/run";
import { observeRuns, runStore, type RunState } from "@/lib/run-store";
import { createRunAdapter, type RunAdapter } from "@/lib/run-adapter";

/**
 * Project the topology snapshot into runs and fold every transition into the
 * run store, so the observed feed has no gaps when a run detail is closed.
 */
export function useRuns(): Run[] {
  const { snapshot } = useAppState();
  return useMemo(() => {
    const runs = buildRuns(snapshot);
    observeRuns(runs);
    return runs;
  }, [snapshot]);
}

export function useRunGroups(runs: Run[]): RunGroup[] {
  return useMemo(() => groupRuns(runs), [runs]);
}

export function useRun(runId: string | undefined): Run | null {
  const runs = useRuns();
  return useMemo(() => findRun(runs, runId), [runs, runId]);
}

/**
 * Subscribe to one run's partition. Topology consumers do not rerender when a
 * high-frequency run update lands, and vice versa.
 */
export function useRunState(runId: string): RunState {
  const subscribe = useMemo(() => (cb: () => void) => runStore.subscribe(runId, cb), [runId]);
  const getSnapshot = useMemo(() => () => runStore.get(runId), [runId]);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

/** The adapter that decides whether a run renders structured or observed. */
export function useRunAdapter(): RunAdapter {
  const { capabilities } = useAppState();
  return useMemo(() => createRunAdapter(capabilities), [capabilities]);
}
