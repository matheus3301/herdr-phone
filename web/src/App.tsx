import { useEffect, useState } from "react";
import { RouterProvider } from "react-router-dom";
import * as api from "@/lib/api";
import { store } from "@/lib/store";
import { applyTheme, prefsStore } from "@/lib/prefs";
import { gateAfterSessionFailure, relayModeFromRejection } from "@/lib/relay-mode";
import { PairingScreen } from "@/routes/pairing";
import { ReauthScreen } from "@/routes/reauth";
import { BootSplash } from "@/components/boot-splash";
import { router } from "@/router";

/**
 * `unpaired` shows the pairing form (quick mode's only gate); `reauth` shows the
 * named-mode reconnect screen, where the remedy is renewing Cloudflare Access, not
 * pasting a pairing secret (DELIVERY-v0.3.0 §7).
 */
type Phase = "booting" | "unpaired" | "reauth" | "pairing" | "paired" | "error";

/**
 * Where a failed pairing attempt lands. A secret the relay rejected is the
 * operator's to fix on the pairing form; an attempt the *origin* refused because
 * Access is no longer valid has the same remedy as a failed boot — renew the edge
 * identity — so only the relay's own Access rejection sends them there, never a
 * remembered mode (which would hide a genuine pairing error).
 */
function phaseForPairFailure(err: unknown): Phase {
  return relayModeFromRejection(err) === "named" ? "reauth" : "unpaired";
}

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
          setPhase(phaseForPairFailure(err));
        }
        return;
      }
      // No fragment: can this device hold a session? GET /session returns the CSRF
      // token + expiry, so a cold reload recovers a fully mutable session
      // (store.start fetches and applies it). In named mode the relay provisions
      // that session from the Access identity it re-validated at the origin, so
      // this succeeds with no pairing at all (SPEC §9.1) — which is exactly why
      // the pairing form below is quick mode's gate only.
      try {
        await api.getSession();
        if (cancelled) return;
        await store.start();
        if (!cancelled) setPhase("paired");
      } catch (err) {
        if (cancelled) return;
        // Pairing is not the remedy in named mode: the gate is Cloudflare Access,
        // so the operator has to re-clear the edge, not paste a secret.
        if (gateAfterSessionFailure(err) === "named") {
          setError(err instanceof api.ApiError ? err.message : null);
          setPhase("reauth");
        } else {
          setPhase("unpaired");
        }
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
      setPhase(phaseForPairFailure(err));
    }
  };

  if (phase === "booting" || phase === "pairing") {
    return <BootSplash label={phase === "pairing" ? "Pairing…" : "Connecting to the relay…"} />;
  }
  if (phase === "reauth") {
    return (
      <ReauthScreen
        error={error}
        onReload={() => window.location.reload()}
        onUsePairing={() => {
          setError(null);
          setPhase("unpaired");
        }}
      />
    );
  }
  if (phase === "unpaired" || phase === "error") {
    return <PairingScreen onPair={pairManually} error={error} />;
  }
  return <RouterProvider router={router} />;
}
