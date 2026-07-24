import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useAppState } from "@/hooks/use-app-store";
import { findRun, groupRuns, type Run, type RunGroup } from "@/lib/run";
import { runSource, type RunListState } from "@/lib/run-source";
import { runStore, type RunState } from "@/lib/run-store";
import { createRunAdapter, type RunAdapter, type RunOutputResult } from "@/lib/run-adapter";

/**
 * The run list from whichever authority the relay advertises: the structured
 * run contract when it is supported, the topology projection otherwise. The
 * decision, the fetch, and the observed-transition folding all live in the run
 * source, so components see one shape either way.
 */
export function useRunList(): RunListState {
  return useSyncExternalStore(runSource.subscribe, runSource.getState, runSource.getState);
}

export function useRuns(): Run[] {
  return useRunList().runs;
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

export interface RunOutputView {
  result: RunOutputResult | null;
  loading: boolean;
  reload: () => void;
}

/**
 * Read one run's bounded terminal output.
 *
 * The generation the run was resolved at is asserted on every read, so a
 * recycled pane returns `generation_stale` instead of another occupant's
 * output. The result is never cached: each mount and each explicit refresh
 * reads fresh, and nothing is written to storage or the service worker.
 */
export function useRunOutput(run: Run): RunOutputView {
  const adapter = useRunAdapter();
  const [result, setResult] = useState<RunOutputResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [nonce, setNonce] = useState(0);
  const paneId = run.paneId;
  const generation = run.generation;
  // A run that has left the list must not keep reading; the route freezes it.
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    const controller = new AbortController();
    setLoading(true);
    void adapter.readRunOutput({ paneId, generation }, controller.signal).then((next) => {
      if (controller.signal.aborted || !alive.current) return;
      setResult(next);
      setLoading(false);
    });
    return () => {
      alive.current = false;
      controller.abort();
    };
  }, [adapter, paneId, generation, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { result, loading, reload };
}
