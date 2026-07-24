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
import { selectionStore } from "@/lib/selection";
import { rootPaneId } from "@/lib/mutation-result";

/** Create a workspace with a label + a confined cwd (SPEC §15). */
export function CreateWorkspaceSheet({ trigger }: { trigger: ReactNode }) {
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [label, setLabel] = useState("");
  const [cwd, setCwd] = useState("/Users/dev/code");

  async function submit() {
    const res = await run("workspace.create", { label: label.trim() || undefined, cwd });
    if (res && !("error" in res && res.error)) {
      const paneId = rootPaneId(res);
      if (paneId) selectionStore.set(paneId);
      setOpen(false);
      setLabel("");
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="create-ws-desc">
        <SheetHeader>
          <SheetTitle>New workspace</SheetTitle>
          <SheetDescription id="create-ws-desc">
            Creates a workspace with its first tab and a shell pane.
          </SheetDescription>
        </SheetHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ws-label">Label</Label>
            <Input
              id="ws-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. space-api"
              autoComplete="off"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Working directory</Label>
            <DirectoryPicker value={cwd} onChange={setCwd} />
          </div>
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
            <Button variant="primary" onClick={() => void submit()} disabled={pending}>
              {pending ? "Creating…" : "Create workspace"}
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

/** Create a tab in a workspace (SPEC §15). */
export function CreateTabSheet({ workspaceId, trigger }: { workspaceId: string; trigger: ReactNode }) {
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [label, setLabel] = useState("");

  async function submit() {
    const res = await run("tab.create", { workspace_id: workspaceId, label: label.trim() || undefined });
    if (res && !("error" in res && res.error)) {
      const paneId = rootPaneId(res);
      if (paneId) selectionStore.set(paneId);
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
            <Label htmlFor="tab-label">Label</Label>
            <Input
              id="tab-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. tests"
              autoComplete="off"
            />
          </div>
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
            <Button variant="primary" onClick={() => void submit()} disabled={pending}>
              {pending ? "Creating…" : "Create tab"}
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}
