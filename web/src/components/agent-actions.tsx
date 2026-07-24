import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Bot, Send } from "lucide-react";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useMutations } from "@/hooks/use-mutations";
import { useAppState } from "@/hooks/use-app-store";
import { assessDanger } from "@/lib/danger";
import { isValidAgentName, suggestAgentName } from "@/lib/agent-name";
import { cn } from "@/lib/utils";
import type { Agent } from "@/lib/types";

/** Prompt an agent (SPEC §15) — the discrete prompt path from the herd. */
export function AgentPromptSheet({ agent, trigger }: { agent: Agent; trigger: ReactNode }) {
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const [armed, setArmed] = useState(false);
  const danger = assessDanger(text);

  async function submit() {
    if (!text.trim()) return;
    if (danger.danger && !armed) {
      setArmed(true);
      return;
    }
    const res = await run("agent.prompt", { pane_id: agent.paneId, text });
    if (res && !("error" in res && res.error)) {
      setOpen(false);
      setText("");
      setArmed(false);
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="prompt-desc">
        <SheetHeader>
          <SheetTitle>Prompt {agent.name}</SheetTitle>
          <SheetDescription id="prompt-desc">
            Sends text and Enter to the agent. Review before sending — this drives a real shell.
          </SheetDescription>
        </SheetHeader>
        <div className="flex flex-col gap-2">
          <Label htmlFor="prompt-text">Message</Label>
          <textarea
            id="prompt-text"
            rows={4}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setArmed(false);
            }}
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            className={cn(
              "resize-none rounded-[10px] border bg-hull px-3 py-2 text-sm text-mist focus-visible:outline-2 focus-visible:outline-brass",
              danger.danger ? "border-flare/60" : "border-frame",
            )}
            placeholder="Approve, continue, or type instructions…"
          />
          {danger.danger && (
            <p className="text-[12px] text-flare" role="status">
              Danger: {danger.reason}. Tap Send again to confirm.
            </p>
          )}
          {error && (
            <p className="text-[13px] text-flare" role="alert">
              {error}
            </p>
          )}
          <div className="mt-1 flex justify-end gap-2">
            <SheetClose asChild>
              <Button variant="outline" disabled={pending}>
                Cancel
              </Button>
            </SheetClose>
            <Button
              variant={danger.danger && armed ? "danger" : "primary"}
              onClick={() => void submit()}
              disabled={pending || !text.trim()}
            >
              <Send className="size-4" /> {pending ? "Sending…" : danger.danger && armed ? "Confirm send" : "Send"}
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

/**
 * Send validated logical keys to an agent (SPEC §15) via agent.send_keys — the
 * validated agent-level path that works from the herd without an open terminal.
 * Every key here is in Herdr's accepted grammar (internal/herdr/keys.go).
 */
const AGENT_KEYS: Array<{ key: string; label: string }> = [
  { key: "enter", label: "Enter" },
  { key: "escape", label: "Esc" },
  { key: "tab", label: "Tab" },
  { key: "shift+tab", label: "⇧Tab" },
  { key: "ctrl+c", label: "^C" },
  { key: "ctrl+d", label: "^D" },
  { key: "up", label: "↑" },
  { key: "down", label: "↓" },
  { key: "left", label: "←" },
  { key: "right", label: "→" },
  { key: "y", label: "y" },
  { key: "n", label: "n" },
];

export function AgentKeysSheet({ agent, trigger }: { agent: Agent; trigger: ReactNode }) {
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [sent, setSent] = useState<string | null>(null);

  async function sendKey(key: string) {
    setSent(null);
    const res = await run("agent.send_keys", { pane_id: agent.paneId, keys: [key] });
    if (res && !("error" in res && res.error)) setSent(key);
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="keys-desc">
        <SheetHeader>
          <SheetTitle>Send keys to {agent.name}</SheetTitle>
          <SheetDescription id="keys-desc">
            Validated logical keys delivered straight to the agent — no open terminal needed.
          </SheetDescription>
        </SheetHeader>
        <div className="grid grid-cols-4 gap-2">
          {AGENT_KEYS.map((k) => (
            <Button
              key={k.key}
              variant={k.key === "ctrl+c" ? "outline" : "default"}
              size="key"
              disabled={pending}
              className={cn("font-utility", k.key === "ctrl+c" && "text-flare")}
              aria-label={`Send ${k.label}`}
              onClick={() => void sendKey(k.key)}
            >
              {k.label}
            </Button>
          ))}
        </div>
        {sent && (
          <p className="text-[12px] text-tide" role="status" aria-live="polite">
            Sent {sent}
          </p>
        )}
        {error && (
          <p className="text-[13px] text-flare" role="alert">
            {error}
          </p>
        )}
      </SheetContent>
    </Sheet>
  );
}

/**
 * Start a discovered agent kind in a shell pane (SPEC §15). Kinds come from
 * server capabilities (never hard-coded); a valid, unique name is required and
 * suggested from the kind so the call never fails the backend's name validation.
 */
export function StartAgentSheet({ paneId, trigger, onDone }: { paneId: string; trigger: ReactNode; onDone?: () => void }) {
  const { capabilities, snapshot } = useAppState();
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [nameEdited, setNameEdited] = useState(false);

  const kinds = capabilities?.agentKinds ?? [];
  const kindsAvailable = capabilities?.agentKindsAvailable ?? false;
  const existingNames = useMemo(() => (snapshot?.agents ?? []).map((a) => a.name), [snapshot]);
  const generation = useMemo(() => snapshot?.panes.find((p) => p.id === paneId)?.generation, [snapshot, paneId]);

  // Suggest a unique name from the chosen kind until the user edits it.
  useEffect(() => {
    if (kind && !nameEdited) setName(suggestAgentName(kind, existingNames));
  }, [kind, nameEdited, existingNames]);

  const nameValid = isValidAgentName(name);
  const nameUnique = !existingNames.includes(name);
  const canStart = !!kind && nameValid && nameUnique && kindsAvailable && !pending;

  async function submit() {
    if (!canStart || !kind) return;
    const res = await run("agent.start", { pane_id: paneId, kind, name }, { expectedGeneration: generation });
    if (res && !("error" in res && res.error)) {
      setOpen(false);
      setKind(null);
      setName("");
      setNameEdited(false);
      onDone?.();
    }
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) {
          setKind(null);
          setName("");
          setNameEdited(false);
        }
      }}
    >
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="start-desc">
        <SheetHeader>
          <SheetTitle>Start an agent</SheetTitle>
          <SheetDescription id="start-desc">Launches a discovered agent kind in this shell pane.</SheetDescription>
        </SheetHeader>
        {!kindsAvailable ? (
          <p className="py-4 text-sm text-muted-ink">
            No agent kinds were discovered from the server, so an agent can't be started right now.
          </p>
        ) : (
          <>
            <Label>Kind</Label>
            <div className="grid grid-cols-2 gap-2">
              {kinds.map((k) => (
                <button
                  key={k}
                  type="button"
                  onClick={() => setKind(k)}
                  aria-pressed={kind === k}
                  className={cn(
                    "flex min-h-11 items-center gap-2 rounded-[10px] border px-3 text-sm focus-visible:outline-2 focus-visible:outline-brass",
                    kind === k ? "border-brass bg-brass/15 text-mist" : "border-frame bg-hull text-muted-ink hover:text-mist",
                  )}
                >
                  <Bot className="size-4" /> {k}
                </button>
              ))}
            </div>
            <div className="mt-1 flex flex-col gap-1.5">
              <Label htmlFor="agent-name">Name</Label>
              <Input
                id="agent-name"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  setNameEdited(true);
                }}
                placeholder="e.g. claude"
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
                aria-invalid={!!name && (!nameValid || !nameUnique)}
              />
              {name && !nameValid && (
                <p className="text-[12px] text-flare" role="alert">
                  Use lowercase letters, digits, - or _, starting with a letter (max 32).
                </p>
              )}
              {name && nameValid && !nameUnique && (
                <p className="text-[12px] text-flare" role="alert">
                  An agent named "{name}" already exists.
                </p>
              )}
            </div>
          </>
        )}
        {error && (
          <p className="text-[13px] text-flare" role="alert">
            {error}
          </p>
        )}
        <div className="mt-2 flex justify-end gap-2">
          <SheetClose asChild>
            <Button variant="outline" disabled={pending}>
              Cancel
            </Button>
          </SheetClose>
          <Button variant="primary" onClick={() => void submit()} disabled={!canStart}>
            {pending ? "Starting…" : "Start agent"}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}
