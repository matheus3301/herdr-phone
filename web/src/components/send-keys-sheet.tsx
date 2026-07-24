import { useState, type ReactNode } from "react";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import { useMutations } from "@/hooks/use-mutations";
import type { PaneTarget } from "@/lib/pane-ops";

/**
 * Validated logical keys, delivered without opening a console.
 *
 * Every key here is in Herdr's accepted grammar, and the request is addressed by
 * pane id + generation. This is deliberately *not* an approval mechanism: a
 * structured interaction response must be bound to an interaction and an opaque
 * choice id, never inferred from a raw `y` keystroke, so nothing here is labelled
 * "approve" or "deny".
 */
const KEYS: Array<{ key: string; label: string; hint?: string }> = [
  { key: "enter", label: "Enter" },
  { key: "escape", label: "Esc" },
  { key: "tab", label: "Tab" },
  { key: "shift+tab", label: "Shift+Tab" },
  { key: "up", label: "Up" },
  { key: "down", label: "Down" },
  { key: "left", label: "Left" },
  { key: "right", label: "Right" },
  { key: "y", label: "y" },
  { key: "n", label: "n" },
  { key: "ctrl+c", label: "Ctrl+C", hint: "interrupt" },
  { key: "ctrl+d", label: "Ctrl+D", hint: "end of input" },
];

export function SendKeysSheet({ run, trigger }: { run: PaneTarget & { agentName: string }; trigger: ReactNode }) {
  const { runPane, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [sent, setSent] = useState<string | null>(null);

  async function press(key: string) {
    setSent(null);
    const res = await runPane("agent.send_keys", run, { keys: [key] });
    if (res && !("error" in res && res.error)) setSent(key);
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="keys-desc">
        <SheetHeader>
          <SheetTitle>Send a key to {run.agentName}</SheetTitle>
          <SheetDescription id="keys-desc">
            Keys go straight to the agent's input. What they mean is up to whatever the agent is showing — open the
            console if you need to see it.
          </SheetDescription>
        </SheetHeader>
        <div className="grid grid-cols-3 gap-2">
          {KEYS.map((k) => (
            <Button
              key={k.key}
              variant="outline"
              size="key"
              disabled={pending}
              aria-label={k.hint ? `Send ${k.label} (${k.hint})` : `Send ${k.label}`}
              onClick={() => void press(k.key)}
              className={k.key === "ctrl+c" ? "text-flare" : undefined}
            >
              {k.label}
            </Button>
          ))}
        </div>
        {sent && (
          <p className="text-meta text-tide" role="status" aria-live="polite">
            Sent {sent}
          </p>
        )}
        {error && (
          <p className="text-meta text-flare" role="alert">
            {error}
          </p>
        )}
      </SheetContent>
    </Sheet>
  );
}
