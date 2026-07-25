import { useState, type ReactNode } from "react";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useMutations } from "@/hooks/use-mutations";
import { isValidAgentName } from "@/lib/agent-name";
import type { PaneTarget } from "@/lib/pane-ops";
import type { MutationOperation, MutationResponse } from "@/lib/types";

interface FormProps {
  title: string;
  description: string;
  fieldLabel: string;
  current: string;
  trigger: ReactNode;
  validate?: (value: string) => string | null;
  submit: (value: string) => Promise<MutationResponse | null>;
  onDone?: () => void;
}

function RenameForm({ title, description, fieldLabel, current, trigger, validate, submit, onDone }: FormProps) {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState(current);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const invalid = validate?.(value) ?? null;

  async function save() {
    const next = value.trim();
    if (!next || invalid) return;
    setPending(true);
    setError(null);
    const res = await submit(next);
    setPending(false);
    if (res && !("error" in res && res.error)) {
      setOpen(false);
      onDone?.();
      return;
    }
    setError(res && "error" in res && res.error ? res.error.message : "The rename could not be sent.");
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) {
          setValue(current);
          setError(null);
        }
      }}
    >
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent aria-describedby="rename-desc">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription id="rename-desc">{description}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rename-input">{fieldLabel}</Label>
          <Input
            id="rename-input"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            aria-invalid={!!invalid}
            onKeyDown={(e) => {
              if (e.key === "Enter") void save();
            }}
          />
          {invalid && (
            <p className="text-meta text-flare" role="alert">
              {invalid}
            </p>
          )}
        </div>
        {error && (
          <p className="mt-2 text-meta text-flare" role="alert">
            {error}
          </p>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={pending}>
              Cancel
            </Button>
          </DialogClose>
          <Button variant="primary" onClick={() => void save()} disabled={pending || !value.trim() || !!invalid}>
            {pending ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Rename a workspace or a tab — resources the relay keys by their own id. */
export function RenameDialog({
  title,
  operation,
  idKey,
  idValue,
  current,
  trigger,
  onDone,
}: {
  title: string;
  operation: Extract<MutationOperation, "workspace.rename" | "tab.rename">;
  idKey: "workspace_id" | "tab_id";
  idValue: string;
  current: string;
  trigger: ReactNode;
  onDone?: () => void;
}) {
  const { run } = useMutations();
  return (
    <RenameForm
      title={title}
      description="Choose a new label."
      fieldLabel="Label"
      current={current}
      trigger={trigger}
      onDone={onDone}
      submit={(value) => run(operation, { [idKey]: idValue, label: value })}
    />
  );
}

/**
 * Rename a pane. Pane-scoped, so it carries the canonical pane id and the
 * current lifecycle generation — a rename against a recycled pane is refused.
 */
export function RenamePaneDialog({
  target,
  current,
  trigger,
  onDone,
}: {
  target: PaneTarget;
  current: string;
  trigger: ReactNode;
  onDone?: () => void;
}) {
  const { runPane } = useMutations();
  return (
    <RenameForm
      title="Rename pane"
      description="Choose a new label for this pane."
      fieldLabel="Label"
      current={current}
      trigger={trigger}
      onDone={onDone}
      submit={(value) => runPane("pane.rename", target, { label: value })}
    />
  );
}

/**
 * Rename an agent. Herdr validates the name (`^[a-z][a-z0-9_-]{0,31}$`), and the
 * request is addressed by pane id + generation — never by the mutable agent
 * name, which the relay would refuse as a divergent identifier anyway.
 */
export function RenameAgentDialog({
  run,
  trigger,
  onDone,
}: {
  run: PaneTarget & { agentName: string };
  trigger: ReactNode;
  onDone?: () => void;
}) {
  const { runPane } = useMutations();
  return (
    <RenameForm
      title="Rename agent"
      description="Lowercase letters, digits, - or _, starting with a letter."
      fieldLabel="Name"
      current={run.agentName}
      trigger={trigger}
      onDone={onDone}
      validate={(value) =>
        !value.trim() || isValidAgentName(value.trim())
          ? null
          : "Use lowercase letters, digits, - or _, starting with a letter (max 32)."
      }
      submit={(value) => runPane("agent.rename", run, { name: value })}
    />
  );
}
