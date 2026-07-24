import { useState } from "react";
import { Minus, Plus, LogOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { useAppState } from "@/hooks/use-app-store";
import { usePrefs } from "@/hooks/use-prefs";
import { prefsStore, type ThemeSetting } from "@/lib/prefs";
import * as api from "@/lib/api";
import { store } from "@/lib/store";
import { relativeTime } from "@/lib/format";
import { cn } from "@/lib/utils";

const THEMES: { value: ThemeSetting; label: string }[] = [
  { value: "system", label: "System" },
  { value: "dark", label: "Dark" },
  { value: "light", label: "Light" },
];

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-seam py-2 last:border-0">
      <span className="text-sm text-muted-ink">{label}</span>
      <span className="truncate font-utility text-[13px] text-mist">{value}</span>
    </div>
  );
}

export function SettingsRoute() {
  const { session, capabilities, snapshot, connection } = useAppState();
  const prefs = usePrefs();
  const [ending, setEnding] = useState(false);

  async function endSession() {
    setEnding(true);
    try {
      await api.endSession(session?.csrfToken ?? "");
    } catch {
      /* ignore; we clear locally regardless */
    }
    store.stop();
    // Return to the pairing screen by reloading into an unpaired state.
    window.location.reload();
  }

  return (
    <div className="h-full overflow-y-auto px-4 py-4">
      <section className="rounded-[12px] border border-frame bg-hull p-4">
        <h2 className="mb-2 font-utility text-[11px] uppercase tracking-wider text-muted-ink">Appearance</h2>
        <Label>Theme</Label>
        <div className="mt-1.5 grid grid-cols-3 gap-2" role="radiogroup" aria-label="Theme">
          {THEMES.map((t) => (
            <button
              key={t.value}
              type="button"
              role="radio"
              aria-checked={prefs.theme === t.value}
              onClick={() => prefsStore.set({ theme: t.value })}
              className={cn(
                "min-h-11 rounded-[10px] border text-sm focus-visible:outline-2 focus-visible:outline-brass",
                prefs.theme === t.value ? "border-brass bg-brass/15 text-mist" : "border-frame bg-bulkhead text-muted-ink",
              )}
            >
              {t.label}
            </button>
          ))}
        </div>

        <Label className="mt-4 block">Terminal font size</Label>
        <div className="mt-1.5 flex items-center gap-3">
          <Button
            variant="outline"
            size="icon"
            aria-label="Decrease font size"
            onClick={() => prefsStore.set({ terminalFontSize: Math.max(9, prefs.terminalFontSize - 1) })}
          >
            <Minus className="size-4" />
          </Button>
          <span className="font-utility text-base text-mist" aria-live="polite">
            {prefs.terminalFontSize}px
          </span>
          <Button
            variant="outline"
            size="icon"
            aria-label="Increase font size"
            onClick={() => prefsStore.set({ terminalFontSize: Math.min(22, prefs.terminalFontSize + 1) })}
          >
            <Plus className="size-4" />
          </Button>
        </div>
      </section>

      <section className="mt-3 rounded-[12px] border border-frame bg-hull p-4">
        <h2 className="mb-1 font-utility text-[11px] uppercase tracking-wider text-muted-ink">Connection</h2>
        <Row label="Operator" value={session?.operator ?? "—"} />
        <Row label="Mode" value={capabilities?.mode ?? session?.mode ?? "—"} />
        <Row label="Access identity" value={capabilities?.accessEnforced ? "enforced" : "not enforced (quick)"} />
        <Row label="Herdr" value={capabilities ? `v${capabilities.herdrVersion} · protocol ${capabilities.herdrProtocol}` : "—"} />
        <Row label="Link" value={connection} />
        <Row label="Agents" value={snapshot ? String(snapshot.agents.length) : "—"} />
        <Row
          label="Session expires"
          value={session && session.expiresUnixMs > 0 ? relativeTime(Date.now(), session.expiresUnixMs) : "—"}
        />
      </section>

      {capabilities && (
        <section className="mt-3 rounded-[12px] border border-frame bg-hull p-4">
          <h2 className="mb-2 font-utility text-[11px] uppercase tracking-wider text-muted-ink">Discovered agent kinds</h2>
          <div className="flex flex-wrap gap-1.5">
            {capabilities.agentKinds.map((k) => (
              <Badge key={k} tone="mist">
                {k}
              </Badge>
            ))}
          </div>
        </section>
      )}

      <section className="mt-3 rounded-[12px] border border-frame bg-hull p-4">
        <h2 className="mb-1 font-utility text-[11px] uppercase tracking-wider text-muted-ink">About</h2>
        <Row label="Herdr Phone" value={`v${__APP_VERSION__}`} />
        <p className="mt-2 text-[13px] text-muted-ink">
          Remote shell-equivalent access to your Mac. Treat the pairing link like a login credential.
        </p>
      </section>

      <Button variant="danger" className="mt-4 w-full justify-center gap-2" onClick={() => void endSession()} disabled={ending}>
        <LogOut className="size-4" /> {ending ? "Ending…" : "End session"}
      </Button>
    </div>
  );
}
