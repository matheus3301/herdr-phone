import { useSyncExternalStore } from "react";
import { prefsStore, type Prefs } from "@/lib/prefs";

export function usePrefs(): Prefs {
  return useSyncExternalStore(prefsStore.subscribe, prefsStore.get, prefsStore.get);
}
