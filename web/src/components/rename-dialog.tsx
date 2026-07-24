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
import type { MutationOperation } from "@/lib/types";

interface RenameDialogProps {
  title: string;
  operation: Extract<MutationOperation, "workspace.rename" | "tab.rename" | "pane.rename" | "agent.rename">;
  /** The id param key expected by the relay for this operation. */
  idKey: "workspace_id" | "tab_id" | "pane_id" | "target";
  idValue: string;
  /** The value param key ("label" for topology, "name" for agent). */
  valueKey: "label" | "name";
  current: string;
  trigger: ReactNode;
  onDone?: () => void;
}

export function RenameDialog({
  title,
  operation,
  idKey,
  idValue,
  valueKey,
  current,
  trigger,
  onDone,
}: RenameDialogProps) {
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState(current);

  async function submit() {
    const res = await run(operation, { [idKey]: idValue, [valueKey]: value.trim() });
    if (res && !("error" in res && res.error)) {
      setOpen(false);
      onDone?.();
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) setValue(current);
      }}
    >
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent aria-describedby="rename-desc">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription id="rename-desc">Enter a new name.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rename-input">Name</Label>
          <Input
            id="rename-input"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            autoComplete="off"
            onKeyDown={(e) => {
              if (e.key === "Enter") void submit();
            }}
          />
        </div>
        {error && (
          <p className="mt-2 text-[13px] text-flare" role="alert">
            {error}
          </p>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={pending}>
              Cancel
            </Button>
          </DialogClose>
          <Button variant="primary" onClick={() => void submit()} disabled={pending || !value.trim()}>
            {pending ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
