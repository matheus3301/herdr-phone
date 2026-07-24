import { useState, type ReactNode } from "react";
import { GitBranch, Plus, FolderOpen, Trash2 } from "lucide-react";
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
import { Badge } from "@/components/ui/badge";
import { DirectoryPicker } from "@/components/directory-picker";
import { ConfirmAction } from "@/components/confirm-action";
import { useAppState } from "@/hooks/use-app-store";
import { useMutations } from "@/hooks/use-mutations";
import { shortPath } from "@/lib/format";

/**
 * List, create, open, and remove worktrees (SPEC §15). The backend snapshot has
 * no "dirty" flag and worktree.remove takes the *workspace* the worktree is open
 * in — so removal is offered only for open worktrees, and a refused removal
 * escalates to worktree.remove_force (the explicit second confirmation).
 */
export function WorktreeSheet({ trigger }: { trigger: ReactNode }) {
  const { snapshot } = useAppState();
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [branch, setBranch] = useState("");
  const [base, setBase] = useState("main");
  const [cwd, setCwd] = useState("/Users/dev/code");
  const worktrees = snapshot?.worktrees ?? [];

  async function create() {
    const res = await run("worktree.create", { cwd, branch: branch.trim(), base: base.trim(), label: branch.trim() });
    if (res && !("error" in res && res.error)) {
      setCreating(false);
      setBranch("");
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="wt-desc">
        <SheetHeader>
          <SheetTitle>Worktrees</SheetTitle>
          <SheetDescription id="wt-desc">Git checkouts backing your workspaces.</SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-2">
          {worktrees.length === 0 && !creating && <p className="py-2 text-sm text-muted-ink">No worktrees yet.</p>}
          {worktrees.map((wt) => (
            <div key={wt.path} className="flex items-center gap-2 rounded-[10px] border border-frame bg-hull p-2 pr-1.5">
              <GitBranch className="size-4 shrink-0 text-muted-ink" />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate text-sm text-mist">{wt.branch ?? wt.label ?? wt.path}</span>
                  {wt.isDetached && <Badge tone="brass">detached</Badge>}
                  {wt.isPrunable && <Badge tone="flare">prunable</Badge>}
                </div>
                <span className="block truncate font-utility text-[11px] text-muted-ink" title={wt.path}>
                  {shortPath(wt.path, 3)}
                </span>
              </div>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Open ${wt.branch ?? wt.path}`}
                onClick={() => run("worktree.open", { path: wt.path })}
              >
                <FolderOpen className="size-4" />
              </Button>
              {wt.removable ? (
                <ConfirmAction
                  operation="worktree.remove"
                  resourceId={wt.openWorkspaceId as string}
                  label={wt.branch ?? wt.label ?? wt.path}
                  params={{ worktree_id: wt.openWorkspaceId }}
                  escalateOperation="worktree.remove_force"
                  trigger={
                    <Button variant="ghost" size="icon" aria-label={`Remove ${wt.branch ?? wt.path}`}>
                      <Trash2 className="size-4 text-flare" />
                    </Button>
                  }
                />
              ) : (
                <Button variant="ghost" size="icon" disabled aria-label="Open the worktree to remove it" title="Open the worktree in a workspace to remove it">
                  <Trash2 className="size-4 text-muted-ink" />
                </Button>
              )}
            </div>
          ))}
        </div>

        {creating ? (
          <div className="mt-1 flex flex-col gap-3 rounded-[10px] border border-frame bg-hull p-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="wt-branch">Branch</Label>
              <Input id="wt-branch" value={branch} onChange={(e) => setBranch(e.target.value)} placeholder="feature/x" autoComplete="off" />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="wt-base">Base</Label>
              <Input id="wt-base" value={base} onChange={(e) => setBase(e.target.value)} autoComplete="off" />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>Source repository</Label>
              <DirectoryPicker value={cwd} onChange={setCwd} />
            </div>
            {error && (
              <p className="text-[13px] text-flare" role="alert">
                {error}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setCreating(false)} disabled={pending}>
                Cancel
              </Button>
              <Button variant="primary" onClick={() => void create()} disabled={pending || !branch.trim()}>
                {pending ? "Creating…" : "Create worktree"}
              </Button>
            </div>
          </div>
        ) : (
          <div className="mt-1 flex justify-between gap-2">
            <Button variant="outline" className="flex-1 justify-center gap-2" onClick={() => setCreating(true)}>
              <Plus className="size-4" /> New worktree
            </Button>
            <SheetClose asChild>
              <Button variant="ghost">Done</Button>
            </SheetClose>
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}
