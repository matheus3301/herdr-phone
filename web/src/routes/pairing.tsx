import { useState } from "react";
import { ShieldAlert, Link2 } from "lucide-react";
import { BrandMark } from "@/components/brand-mark";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { extractPairingSecret } from "@/lib/pairing";

/**
 * Pairing screen (SPEC §9.1, §14). Shown before the app when unpaired. The
 * operator runs `herdr-phone setup-link` on the Mac and opens the printed URL,
 * or pastes it here. Honest remote-shell warning per SPEC §1.
 */
export function PairingScreen({
  onPair,
  error,
}: {
  onPair: (secret: string) => void;
  error: string | null;
}) {
  const [value, setValue] = useState("");
  const secret = extractPairingSecret(value);

  return (
    <div
      className="mx-auto flex min-h-dvh w-full max-w-md flex-col justify-center gap-6 bg-deck px-6"
      style={{ paddingTop: "var(--spacing-safe-top)", paddingBottom: "var(--spacing-safe-bottom)" }}
    >
      <div className="flex flex-col items-center gap-3 text-center">
        <BrandMark className="size-14" />
        <h1 className="text-xl font-semibold text-mist">Pair this device</h1>
        <p className="text-sm text-muted-ink">
          Run <code className="font-utility text-tide">herdr-phone setup-link</code> on your Mac and open the
          printed link on this phone, or paste it below.
        </p>
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor="pair-input">Pairing link or secret</Label>
        <Input
          id="pair-input"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="https://…/#pair=…"
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          onKeyDown={(e) => {
            if (e.key === "Enter" && secret) onPair(secret);
          }}
        />
        {error && (
          <p className="text-[13px] text-flare" role="alert">
            {error}
          </p>
        )}
        <Button variant="primary" className="mt-1 gap-2" onClick={() => onPair(secret)} disabled={!secret}>
          <Link2 className="size-4" /> Pair device
        </Button>
      </div>

      <div className="flex items-start gap-2 rounded-[10px] border border-flare/40 bg-flare/10 p-3 text-[13px] text-mist">
        <ShieldAlert className="mt-0.5 size-4 shrink-0 text-flare" />
        <p>
          Herdr Phone grants <strong>remote shell-equivalent access</strong> to your Mac. Pair only your own
          devices, and keep the pairing link private — it is single-use and rotates on success.
        </p>
      </div>
    </div>
  );
}
