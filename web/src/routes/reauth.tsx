import { RefreshCw, ShieldAlert } from "lucide-react";
import { BrandMark } from "@/components/brand-mark";
import { Button } from "@/components/ui/button";

/**
 * Named-mode reconnect screen (SPEC §9.1, DELIVERY-v0.3.0 §7).
 *
 * In named mode Cloudflare Access is the gate: the relay provisions the app
 * session from the Access JWT it re-validates at the origin, so a rejected
 * request means the edge identity — not a pairing secret — needs renewing. A
 * top-level reload is the remedy, because only a navigation lets Access hand the
 * browser a fresh identity cookie.
 *
 * The pairing escape hatch stays reachable: `/pair` remains live in named mode for
 * re-binding, and it is also the way out if this device's remembered mode is
 * stale (the relay was reconfigured to a quick tunnel).
 */
export function ReauthScreen({
  error,
  onReload,
  onUsePairing,
}: {
  error: string | null;
  onReload: () => void;
  onUsePairing: () => void;
}) {
  return (
    <div
      className="mx-auto flex min-h-dvh w-full max-w-md flex-col justify-center gap-6 bg-deck px-6"
      style={{ paddingTop: "var(--spacing-safe-top)", paddingBottom: "var(--spacing-safe-bottom)" }}
    >
      <div className="flex flex-col items-center gap-3 text-center">
        <BrandMark className="size-14" />
        <h1 className="text-xl font-semibold text-mist">Sign in to continue</h1>
        <p className="text-sm text-muted-ink">
          This relay is protected by Cloudflare Access, and your Access session is no longer valid. Reload to sign
          in again with your identity provider — no pairing link is needed.
        </p>
      </div>

      <div className="flex flex-col gap-2">
        {error && (
          <p className="text-[13px] text-flare" role="alert">
            {error}
          </p>
        )}
        <Button variant="primary" className="gap-2" onClick={onReload}>
          <RefreshCw className="size-4" /> Reload and sign in
        </Button>
        <Button variant="quiet" onClick={onUsePairing}>
          Use a pairing link instead
        </Button>
      </div>

      <div className="flex items-start gap-2 rounded-[10px] border border-flare/40 bg-flare/10 p-3 text-[13px] text-mist">
        <ShieldAlert className="mt-0.5 size-4 shrink-0 text-flare" />
        <p>
          Herdr Phone grants <strong>remote shell-equivalent access</strong> to your Mac. Only your own Access
          identity can reach it.
        </p>
      </div>
    </div>
  );
}
