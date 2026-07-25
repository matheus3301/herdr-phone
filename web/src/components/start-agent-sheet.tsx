import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useMutations } from "@/hooks/use-mutations";
import { useAppState } from "@/hooks/use-app-store";
import { isValidAgentName, suggestAgentName } from "@/lib/agent-name";
import { checkPaneTarget, type PaneTarget } from "@/lib/pane-ops";
import { cn } from "@/lib/utils";

/**
 * Start a discovered agent kind in an existing shell pane.
 *
 * Kinds come from the relay's capability document, never a hard-coded list, and
 * the name is validated against Herdr's own rule before the call so a malformed
 * or duplicate name never reaches the mutator. The request is pane-scoped and
 * carries the pane's current generation.
 */
export function StartAgentSheet({
  target,
  trigger,
  onDone,
}: {
  target: PaneTarget;
  trigger: ReactNode;
  onDone?: () => void;
}) {
  const { capabilities, snapshot } = useAppState();
  const { runPane, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [nameEdited, setNameEdited] = useState(false);

  const kinds = capabilities?.agentKinds ?? [];
  const kindsAvailable = capabilities?.agentKindsAvailable ?? false;
  const existingNames = useMemo(() => (snapshot?.agents ?? []).map((a) => a.name), [snapshot]);
  const problem = checkPaneTarget(target);

  useEffect(() => {
    if (kind && !nameEdited) setName(suggestAgentName(kind, existingNames));
  }, [kind, nameEdited, existingNames]);

  const nameValid = isValidAgentName(name);
  const nameUnique = !existingNames.includes(name);
  const canStart = !!kind && nameValid && nameUnique && kindsAvailable && !problem && !pending;

  function reset() {
    setKind(null);
    setName("");
    setNameEdited(false);
  }

  async function submit() {
    if (!canStart || !kind) return;
    const res = await runPane("agent.start", target, { kind, name });
    if (res && !("error" in res && res.error)) {
      setOpen(false);
      reset();
      onDone?.();
    }
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}
    >
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="start-agent-desc">
        <SheetHeader>
          <SheetTitle>Start an agent</SheetTitle>
          <SheetDescription id="start-agent-desc">
            Launches a discovered agent kind in pane {target.paneId}.
          </SheetDescription>
        </SheetHeader>

        {problem ? (
          <p className="py-2 text-body text-flare" role="alert">
            {problem.message}
          </p>
        ) : !kindsAvailable ? (
          <p className="py-2 text-body text-muted-ink">
            The relay discovered no agent kinds on your Mac, so nothing can be started from here.
          </p>
        ) : (
          <>
            <div className="flex flex-wrap gap-2" role="radiogroup" aria-label="Agent kind">
              {kinds.map((k) => (
                <button
                  key={k}
                  type="button"
                  role="radio"
                  aria-checked={kind === k}
                  onClick={() => setKind(k)}
                  className={cn(
                    "min-h-11 rounded-log px-3.5 text-body focus-visible:outline-2 focus-visible:outline-brass",
                    kind === k ? "bg-brass/12 text-mist ring-1 ring-brass" : "bg-hull text-muted-ink ring-1 ring-seam hover:text-mist",
                  )}
                >
                  {k}
                </button>
              ))}
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="new-agent-name">Name</Label>
              <Input
                id="new-agent-name"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  setNameEdited(true);
                }}
                placeholder="claude"
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
                aria-invalid={!!name && (!nameValid || !nameUnique)}
              />
              {name && !nameValid && (
                <p className="text-meta text-flare" role="alert">
                  Use lowercase letters, digits, - or _, starting with a letter (max 32).
                </p>
              )}
              {name && nameValid && !nameUnique && (
                <p className="text-meta text-flare" role="alert">
                  An agent named "{name}" already exists.
                </p>
              )}
            </div>
          </>
        )}

        {error && (
          <p className="text-meta text-flare" role="alert">
            {error}
          </p>
        )}

        <div className="flex justify-end gap-2">
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
