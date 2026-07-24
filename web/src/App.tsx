import { useEffect, useState } from "react";
import { RouterProvider } from "react-router-dom";
import * as api from "@/lib/api";
import { store } from "@/lib/store";
import { applyTheme, prefsStore } from "@/lib/prefs";
import { PairingScreen } from "@/routes/pairing";
import { BootSplash } from "@/components/boot-splash";
import { router } from "@/router";

type Phase = "booting" | "unpaired" | "pairing" | "paired" | "error";

/** Extract and consume a `#pair=<secret>` fragment without leaving it in history. */
function consumePairFragment(): string | null {
  const hash = window.location.hash;
  const match = hash.match(/pair=([^&]+)/);
  if (!match) return null;
  const secret = decodeURIComponent(match[1]);
  // Remove the fragment from the URL/history before any network call (SPEC §9.1).
  window.history.replaceState(null, "", window.location.pathname + window.location.search);
  return secret;
}

export function App() {
  const [phase, setPhase] = useState<Phase>("booting");
  const [error, setError] = useState<string | null>(null);

  // Theme: apply now and react to system changes when set to "system".
  useEffect(() => {
    applyTheme(prefsStore.get().theme);
    const mql = window.matchMedia?.("(prefers-color-scheme: light)");
    const onChange = () => {
      if (prefsStore.get().theme === "system") applyTheme("system");
    };
    mql?.addEventListener("change", onChange);
    return () => mql?.removeEventListener("change", onChange);
  }, []);

  useEffect(() => {
    let cancelled = false;
    async function boot() {
      const secret = consumePairFragment();
      if (secret) {
        setPhase("pairing");
        try {
          const pair = await api.pair(secret);
          if (cancelled) return;
          store.setSessionFromPair(pair);
          await store.start();
          if (!cancelled) setPhase("paired");
        } catch (err) {
          if (cancelled) return;
          setError(err instanceof api.ApiError ? err.message : "Pairing failed");
          setPhase("unpaired");
        }
        return;
      }
      // No fragment: do we already have a session cookie? GET /session confirms
      // it and now returns the CSRF token + expiry, so a cold reload recovers a
      // fully mutable session (store.start fetches and applies it).
      try {
        await api.getSession();
        if (cancelled) return;
        await store.start();
        if (!cancelled) setPhase("paired");
      } catch {
        if (!cancelled) setPhase("unpaired");
      }
    }
    void boot();
    return () => {
      cancelled = true;
    };
  }, []);

  const pairManually = async (secret: string) => {
    setPhase("pairing");
    setError(null);
    try {
      const pair = await api.pair(secret);
      store.setSessionFromPair(pair);
      await store.start();
      setPhase("paired");
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Pairing failed");
      setPhase("unpaired");
    }
  };

  if (phase === "booting" || phase === "pairing") {
    return <BootSplash label={phase === "pairing" ? "Pairing…" : "Connecting to the relay…"} />;
  }
  if (phase === "unpaired" || phase === "error") {
    return <PairingScreen onPair={pairManually} error={error} />;
  }
  return <RouterProvider router={router} />;
}
