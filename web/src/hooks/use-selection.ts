import { useSyncExternalStore } from "react";
import { selectionStore } from "@/lib/selection";

export function useSelectedPaneId(): string | null {
  return useSyncExternalStore(selectionStore.subscribe, selectionStore.get, selectionStore.get);
}
