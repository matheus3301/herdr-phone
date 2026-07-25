import { useState, type ReactNode } from "react";
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
import { DirectoryPicker } from "@/components/directory-picker";
import { useMutations } from "@/hooks/use-mutations";
import { DEFAULT_CWD } from "@/lib/launch";

/** Create a workspace with a label and a relay-confined working directory. */
export function CreateWorkspaceSheet({ trigger, onDone }: { trigger: ReactNode; onDone?: (workspaceId?: string) => void }) {
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [label, setLabel] = useState("");
  const [cwd, setCwd] = useState(DEFAULT_CWD);

  async function submit() {
    const res = await run("workspace.create", { label: label.trim() || undefined, cwd });
    if (res && !("error" in res && res.error)) {
      setOpen(false);
      setLabel("");
      onDone?.();
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="create-ws-desc">
        <SheetHeader>
          <SheetTitle>New workspace</SheetTitle>
          <SheetDescription id="create-ws-desc">
            Herdr opens the directory with a first tab and a shell pane.
          </SheetDescription>
        </SheetHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ws-create-label">Name</Label>
            <Input
              id="ws-create-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="space-api"
              autoComplete="off"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Working directory</Label>
            <DirectoryPicker value={cwd} onChange={setCwd} />
          </div>
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
            <Button variant="primary" onClick={() => void submit()} disabled={pending}>
              {pending ? "Creating…" : "Create workspace"}
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

/** Create a tab (and its root shell pane) in a workspace. */
export function CreateTabSheet({ workspaceId, trigger }: { workspaceId: string; trigger: ReactNode }) {
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [label, setLabel] = useState("");

  async function submit() {
    const res = await run("tab.create", { workspace_id: workspaceId, label: label.trim() || undefined });
    if (res && !("error" in res && res.error)) {
      setOpen(false);
      setLabel("");
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="create-tab-desc">
        <SheetHeader>
          <SheetTitle>New tab</SheetTitle>
          <SheetDescription id="create-tab-desc">Adds a tab with a shell pane to this workspace.</SheetDescription>
        </SheetHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tab-create-label">Name</Label>
            <Input
              id="tab-create-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="tests"
              autoComplete="off"
            />
          </div>
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
            <Button variant="primary" onClick={() => void submit()} disabled={pending}>
              {pending ? "Creating…" : "Create tab"}
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}
