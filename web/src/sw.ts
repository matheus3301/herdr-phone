/// <reference lib="webworker" />
/**
 * Service worker (SPEC §14.4, §16). Precaches ONLY the static shell (JS/CSS/
 * fonts/icons/html) injected at build time. It must never cache /api/ or
 * terminal data — those are always network, no-store. Navigation falls back to
 * the cached index shell so the PWA opens offline into the offline route.
 */
import { precacheAndRoute } from "workbox-precaching";
import { registerRoute, setDefaultHandler, setCatchHandler } from "workbox-routing";

declare const self: ServiceWorkerGlobalScope & { __WB_MANIFEST: Array<{ url: string; revision: string | null }> };

// Precache the build-time shell manifest.
precacheAndRoute(self.__WB_MANIFEST);

// API + realtime endpoints: network only, never cached.
registerRoute(
  ({ url }) => url.pathname.startsWith("/api/"),
  async ({ request }) => fetch(request),
);

// Navigations: try network, fall back to the cached shell (index.html).
registerRoute(
  ({ request }) => request.mode === "navigate",
  async ({ event }) => {
    try {
      return await fetch((event as FetchEvent).request);
    } catch {
      const cache = await caches.open("workbox-precache-v2-" + self.registration.scope);
      const cached = (await cache.match("index.html")) ?? (await caches.match("index.html"));
      return cached ?? Response.error();
    }
  },
);

// Everything else same-origin static: cache-first via precache, else network.
setDefaultHandler(async ({ request }) => {
  const cached = await caches.match(request);
  return cached ?? fetch(request);
});

setCatchHandler(async () => Response.error());

self.addEventListener("message", (event) => {
  if ((event.data as { type?: string })?.type === "SKIP_WAITING") {
    void self.skipWaiting();
  }
});

self.addEventListener("install", () => {
  void self.skipWaiting();
});
self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});
