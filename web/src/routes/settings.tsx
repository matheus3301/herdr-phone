import { useState } from "react";
import { LogOut, Minus, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { useAppState } from "@/hooks/use-app-store";
import { usePrefs } from "@/hooks/use-prefs";
import { useRouteTitle } from "@/hooks/use-route-title";
import { prefsStore, type ThemeSetting } from "@/lib/prefs";
import * as api from "@/lib/api";
import { store } from "@/lib/store";
import { relativeTime } from "@/lib/format";
import { detectRunFidelity } from "@/lib/run-adapter";
import { cn } from "@/lib/utils";

const THEMES: { value: ThemeSetting; label: string }[] = [
  { value: "system", label: "System" },
  { value: "dark", label: "Dark" },
  { value: "light", label: "Light" },
];

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-seam py-2 last:border-0">
      <span className="text-body text-muted-ink">{label}</span>
      <span className="tabular truncate text-mist">{value}</span>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mt-4 first:mt-0">
      <h2 className="mb-1.5 text-body font-semibold text-mist">{title}</h2>
      {children}
    </section>
  );
}

export function SettingsRoute() {
  const heading = useRouteTitle("Settings");
  const { session, capabilities, snapshot, connection } = useAppState();
  const prefs = usePrefs();
  const [ending, setEnding] = useState(false);

  const fidelity = detectRunFidelity(capabilities);
  // Named mode's gate is the Cloudflare Access session, not a pairing link, so
  // the copy has to name what the operator would actually have to revoke.
  const named = session?.mode === "named";

  async function endSession() {
    setEnding(true);
    try {
      await api.endSession(session?.csrfToken ?? "");
    } catch {
      /* clear locally regardless */
    }
    store.stop();
    window.location.reload();
  }

  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 pb-10">
      <h1 ref={heading} tabIndex={-1} className="mb-3 text-prose font-semibold text-mist">
        Settings
      </h1>

      <Section title="Appearance">
        <Label>Theme</Label>
        <div className="mt-1.5 grid grid-cols-3 gap-2" role="radiogroup" aria-label="Theme">
          {THEMES.map((theme) => (
            <button
              key={theme.value}
              type="button"
              role="radio"
              aria-checked={prefs.theme === theme.value}
              onClick={() => prefsStore.set({ theme: theme.value })}
              className={cn(
                "min-h-11 rounded-log text-body focus-visible:outline-2 focus-visible:outline-brass",
                prefs.theme === theme.value
                  ? "bg-brass/12 text-mist ring-1 ring-brass"
                  : "bg-hull text-muted-ink ring-1 ring-seam",
              )}
            >
              {theme.label}
            </button>
          ))}
        </div>

        <Label className="mt-4 block">Console font size</Label>
        <div className="mt-1.5 flex items-center gap-3">
          <Button
            variant="outline"
            size="icon"
            aria-label="Decrease console font size"
            onClick={() => prefsStore.set({ terminalFontSize: Math.max(9, prefs.terminalFontSize - 1) })}
          >
            <Minus className="size-4" />
          </Button>
          <span className="tabular text-mist" aria-live="polite">
            {prefs.terminalFontSize}px
          </span>
          <Button
            variant="outline"
            size="icon"
            aria-label="Increase console font size"
            onClick={() => prefsStore.set({ terminalFontSize: Math.min(22, prefs.terminalFontSize + 1) })}
          >
            <Plus className="size-4" />
          </Button>
        </div>
      </Section>

      <Section title="Connection">
        <Row label="Operator" value={session?.operator ?? "—"} />
        <Row label="Mode" value={capabilities?.mode ?? session?.mode ?? "—"} />
        <Row label="Access identity" value={capabilities?.accessEnforced ? "enforced" : "not enforced (quick mode)"} />
        <Row
          label="Herdr"
          value={capabilities ? `v${capabilities.herdrVersion} · protocol ${capabilities.herdrProtocol}` : "—"}
        />
        <Row label="Link" value={connection} />
        <Row label="Agents" value={snapshot ? String(snapshot.agents.length) : "—"} />
        <Row
          label="Session expires"
          value={session && session.expiresUnixMs > 0 ? relativeTime(Date.now(), session.expiresUnixMs) : "—"}
        />
      </Section>

      <Section title="Run detail">
        <Row label="Source" value={fidelity === "structured" ? "structured runs" : "recent terminal output"} />
        <p className="mt-2 max-w-prose text-meta text-muted-ink">
          {fidelity === "structured"
            ? "The relay publishes structured run messages, so runs render authoritative parts."
            : "This relay publishes no structured run contract, so a run shows the status changes this phone observed plus a bounded tail of what the pane rendered. Nothing is parsed into messages, tool calls, or approvals."}
        </p>
      </Section>

      {capabilities && capabilities.agentKinds.length > 0 && (
        <Section title="Discovered agent kinds">
          <div className="flex flex-wrap gap-1.5">
            {capabilities.agentKinds.map((kind) => (
              <Badge key={kind} tone="mist">
                {kind}
              </Badge>
            ))}
          </div>
        </Section>
      )}

      <Section title="About">
        <Row label="Herdr Phone" value={`v${__APP_VERSION__}`} />
        <p className="mt-2 max-w-prose text-meta text-muted-ink">
          This grants remote shell-equivalent access to your Mac.{" "}
          {named
            ? "Treat your Cloudflare Access session like a login credential."
            : "Treat the pairing link like a login credential."}
        </p>
      </Section>

      <Button variant="danger" className="mt-5 w-full" onClick={() => void endSession()} disabled={ending}>
        <LogOut className="size-4" />{" "}
        {named ? (ending ? "Signing out…" : "Sign out this device") : ending ? "Ending…" : "End session"}
      </Button>
      {named && (
        <p className="mt-2 max-w-prose text-meta text-muted-ink">
          Cloudflare Access signs you back in — revoke the Access session or run herdr-phone stop to end access
        </p>
      )}
    </div>
  );
}
