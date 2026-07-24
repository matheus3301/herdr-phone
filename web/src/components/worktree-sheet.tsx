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

type Mode = "list" | "create" | "open";

/**
 * Create, open, and remove worktrees (SPEC §15).
 *
 * What this sheet lists is exactly what the snapshot knows: the git checkout
 * behind each open workspace (`workspaces[].worktree`). It deliberately does not
 * claim to be a worktree inventory — `session.snapshot` carries no top-level
 * worktree array (SPEC §3.1), so enumerating checkouts that are *not* open would
 * mean inventing them. Opening one is therefore done by path.
 *
 * Removal is offered only for a **linked** worktree: `worktree.remove` takes the
 * workspace the worktree is open in, and git refuses to remove a main checkout.
 * A refused removal escalates to `worktree.remove_force` — the explicit second
 * confirmation — because the backend reports no "dirty" flag to pre-check.
 */
export function WorktreeSheet({ trigger }: { trigger: ReactNode }) {
  const { snapshot } = useAppState();
  const { run, pending, error } = useMutations();
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<Mode>("list");
  const [branch, setBranch] = useState("");
  const [base, setBase] = useState("main");
  const [cwd, setCwd] = useState("/Users/dev/code");
  const checkouts = (snapshot?.workspaces ?? []).filter((w) => w.worktree);

  async function create() {
    const res = await run("worktree.create", { cwd, branch: branch.trim(), base: base.trim(), label: branch.trim() });
    if (res && !("error" in res && res.error)) {
      setMode("list");
      setBranch("");
    }
  }

  async function openExisting() {
    const res = await run("worktree.open", { path: cwd });
    if (res && !("error" in res && res.error)) setMode("list");
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="wt-desc">
        <SheetHeader>
          <SheetTitle>Worktrees</SheetTitle>
          <SheetDescription id="wt-desc">
            The git checkouts behind your open workspaces. Herdr does not report checkouts that are not open.
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-2">
          {checkouts.length === 0 && mode === "list" && (
            <p className="py-2 text-sm text-muted-ink">No open workspace resolves to a git checkout.</p>
          )}
          {checkouts.map((workspace) => {
            const wt = workspace.worktree!;
            return (
              <div
                key={workspace.id}
                className="flex items-center gap-2 rounded-[10px] border border-seam bg-hull p-2 pr-1.5"
              >
                <GitBranch className="size-4 shrink-0 text-muted-ink" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm text-mist">{wt.repoName}</span>
                    <Badge tone={wt.isLinkedWorktree ? "brass" : "neutral"}>
                      {wt.isLinkedWorktree ? "linked" : "main"}
                    </Badge>
                  </div>
                  <span className="block truncate tabular text-muted-ink" title={wt.checkoutPath}>
                    {shortPath(wt.checkoutPath, 3)}
                  </span>
                </div>
                {wt.isLinkedWorktree ? (
                  <ConfirmAction
                    operation="worktree.remove"
                    resourceId={workspace.id}
                    label={wt.repoName}
                    params={{ worktree_id: workspace.id }}
                    escalateOperation="worktree.remove_force"
                    trigger={
                      <Button variant="ghost" size="icon" aria-label={`Remove the worktree in ${workspace.label}`}>
                        <Trash2 className="size-4 text-flare" />
                      </Button>
                    }
                  />
                ) : (
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled
                    aria-label="A repository's main checkout cannot be removed"
                    title="Git refuses to remove a repository's main checkout."
                  >
                    <Trash2 className="size-4 text-muted-ink" />
                  </Button>
                )}
              </div>
            );
          })}
        </div>

        {mode === "create" ? (
          <div className="mt-1 flex flex-col gap-3 rounded-[10px] border border-seam bg-hull p-3">
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
              <Button variant="outline" onClick={() => setMode("list")} disabled={pending}>
                Cancel
              </Button>
              <Button variant="primary" onClick={() => void create()} disabled={pending || !branch.trim()}>
                {pending ? "Creating…" : "Create worktree"}
              </Button>
            </div>
          </div>
        ) : mode === "open" ? (
          <div className="mt-1 flex flex-col gap-3 rounded-[10px] border border-seam bg-hull p-3">
            <div className="flex flex-col gap-1.5">
              <Label>Checkout directory</Label>
              <DirectoryPicker value={cwd} onChange={setCwd} />
            </div>
            {error && (
              <p className="text-[13px] text-flare" role="alert">
                {error}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setMode("list")} disabled={pending}>
                Cancel
              </Button>
              <Button variant="primary" onClick={() => void openExisting()} disabled={pending}>
                {pending ? "Opening…" : "Open worktree"}
              </Button>
            </div>
          </div>
        ) : (
          <div className="mt-1 flex flex-wrap justify-between gap-2">
            <Button variant="outline" className="flex-1 justify-center gap-2" onClick={() => setMode("create")}>
              <Plus className="size-4" /> New worktree
            </Button>
            <Button variant="outline" className="flex-1 justify-center gap-2" onClick={() => setMode("open")}>
              <FolderOpen className="size-4" /> Open existing
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
