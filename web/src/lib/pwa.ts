import { registerSW } from "virtual:pwa-register";

/**
 * Register the service worker (SPEC §14.4). "prompt" strategy: a new build is
 * activated on the next explicit reload rather than yanking the page mid-session.
 * No-op silently over plain HTTP (no secure context) so dev/tests are unaffected.
 */
export function setupPWA(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  try {
    const update = registerSW({
      immediate: true,
      onNeedRefresh() {
        // A future release could surface an in-app "update ready" banner here.
        // v0.1.0 activates on the next natural reload.
      },
    });
    void update;
  } catch {
    // insecure context / unsupported — features degrade, app still runs.
  }
}
