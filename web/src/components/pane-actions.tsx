import { useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  ArrowLeftRight,
  Bot,
  ChevronsLeftRight,
  ChevronsUpDown,
  Columns2,
  Crosshair,
  FolderInput,
  Maximize2,
  Minimize2,
  MoveRight,
  PanelRight,
  Pencil,
  Rows2,
  SquareTerminal,
  Trash2,
} from "lucide-react";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { RenamePaneDialog } from "@/components/rename-dialog";
import { ConfirmAction } from "@/components/confirm-action";
import { StartAgentSheet } from "@/components/start-agent-sheet";
import { useMutations } from "@/hooks/use-mutations";
import { useAppState } from "@/hooks/use-app-store";
import { checkPaneTarget } from "@/lib/pane-ops";
import { formatRunId } from "@/lib/run";
import type { Pane } from "@/lib/types";
import { cn } from "@/lib/utils";

function ActionRow({
  icon,
  label,
  onClick,
  disabled,
  destructive,
}: {
  icon: ReactNode;
  label: string;
  onClick?: () => void;
  disabled?: boolean;
  destructive?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "flex min-h-11 w-full items-center gap-3 rounded-log bg-hull px-3 text-left text-body ring-1 ring-seam",
        "hover:bg-bulkhead disabled:opacity-45 focus-visible:outline-2 focus-visible:outline-brass",
        destructive ? "text-flare" : "text-mist",
      )}
    >
      <span className={cn("shrink-0", destructive ? "text-flare" : "text-muted-ink")}>{icon}</span>
      {label}
    </button>
  );
}

/** Pick a destination tab for `pane.move`. */
function MoveToTabSheet({ pane, trigger, onDone }: { pane: Pane; trigger: ReactNode; onDone?: () => void }) {
  const { snapshot } = useAppState();
  const { runPane } = useMutations();
  const [open, setOpen] = useState(false);
  const targets = (snapshot?.tabs ?? [])
    .filter((t) => t.workspaceId === pane.workspaceId && t.id !== pane.tabId)
    .sort((a, b) => a.order - b.order);

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby={undefined}>
        <SheetHeader>
          <SheetTitle>Move pane to another tab</SheetTitle>
        </SheetHeader>
        {targets.length === 0 ? (
          <p className="py-2 text-body text-muted-ink">This workspace has no other tabs.</p>
        ) : (
          <div className="flex flex-col gap-1.5">
            {targets.map((tab) => (
              <button
                key={tab.id}
                type="button"
                className="flex min-h-11 items-center gap-2 rounded-log bg-hull px-3 text-left text-body text-mist ring-1 ring-seam hover:bg-bulkhead focus-visible:outline-2 focus-visible:outline-brass"
                onClick={async () => {
                  await runPane("pane.move", { paneId: pane.id, generation: pane.generation }, {
                    destination: { type: "tab", tab_id: tab.id },
                  });
                  setOpen(false);
                  onDone?.();
                }}
              >
                <FolderInput className="size-4 text-muted-ink" aria-hidden />
                <span className="flex-1 truncate">{tab.label}</span>
                <span className="tabular text-faint-ink">{tab.id}</span>
              </button>
            ))}
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

/**
 * The complete advanced topology surface for one pane.
 *
 * Every operation here is pane-scoped, so each one carries the canonical pane id
 * and the pane's current lifecycle generation. When the snapshot has no
 * generation for the pane the whole sheet says so and refuses to act, rather
 * than sending requests the relay would reject.
 */
export function PaneActions({ pane, trigger, onClose }: { pane: Pane; trigger: ReactNode; onClose?: () => void }) {
  const { runPane } = useMutations();
  const { snapshot } = useAppState();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);

  const target = { paneId: pane.id, generation: pane.generation };
  const problem = checkPaneTarget(target);
  const label = pane.title ?? pane.agentName ?? pane.id;
  const siblings = (snapshot?.panes ?? []).filter((p) => p.tabId === pane.tabId).sort((a, b) => a.order - b.order);
  const index = siblings.findIndex((p) => p.id === pane.id);
  const swapTarget = siblings.length > 1 ? siblings[(index + 1) % siblings.length] : null;
  const done = () => {
    setOpen(false);
    onClose?.();
  };

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby="pane-actions-desc">
        <SheetHeader>
          <SheetTitle>{label}</SheetTitle>
          <p id="pane-actions-desc" className="tabular text-faint-ink">
            {pane.id} · generation {pane.generation > 0 ? pane.generation : "unknown"}
          </p>
        </SheetHeader>

        {problem ? (
          <p className="py-2 text-body text-flare" role="alert">
            {problem.message}
          </p>
        ) : (
          <>
            <div className="flex flex-col gap-2">
              <ActionRow
                icon={<SquareTerminal className="size-4" />}
                label="Open console"
                onClick={() => navigate(`/console/${encodeURIComponent(pane.id)}?generation=${pane.generation}`)}
              />
              {pane.agentKind ? (
                <ActionRow
                  icon={<Bot className="size-4" />}
                  label="Open this agent's run"
                  onClick={() => navigate(`/runs/${encodeURIComponent(formatRunId(target))}`)}
                />
              ) : (
                <StartAgentSheet
                  target={target}
                  onDone={done}
                  trigger={<ActionRow icon={<Bot className="size-4" />} label="Start an agent here" />}
                />
              )}
              <ActionRow
                icon={<Crosshair className="size-4" />}
                label="Focus this pane on the Mac"
                onClick={() => void runPane("pane.focus", target)}
              />
            </div>

            <div className="grid grid-cols-2 gap-2">
              <ActionRow
                icon={<Columns2 className="size-4" />}
                label="Split right"
                onClick={() => void runPane("pane.split", target, { direction: "right" })}
              />
              <ActionRow
                icon={<Rows2 className="size-4" />}
                label="Split down"
                onClick={() => void runPane("pane.split", target, { direction: "down" })}
              />
              <ActionRow
                icon={pane.zoomed ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
                label={pane.zoomed ? "Unzoom" : "Zoom"}
                onClick={() => void runPane("pane.zoom", target)}
              />
              <ActionRow
                icon={<ArrowLeftRight className="size-4" />}
                label="Swap"
                disabled={!swapTarget}
                onClick={() => swapTarget && void runPane("pane.swap", target, { target_pane_id: swapTarget.id })}
              />
              <ActionRow
                icon={<ChevronsLeftRight className="size-4" />}
                label="Widen"
                onClick={() => void runPane("pane.resize", target, { direction: "right", amount: 4 })}
              />
              <ActionRow
                icon={<ChevronsUpDown className="size-4" />}
                label="Taller"
                onClick={() => void runPane("pane.resize", target, { direction: "down", amount: 4 })}
              />
              <ActionRow
                icon={<PanelRight className="size-4" />}
                label="Move to a new tab"
                onClick={() => void runPane("pane.move", target, { destination: { type: "new_tab" } })}
              />
              <ActionRow
                icon={<MoveRight className="size-4" />}
                label="Move to a new workspace"
                onClick={() => void runPane("pane.move", target, { destination: { type: "new_workspace" } })}
              />
            </div>

            <MoveToTabSheet
              pane={pane}
              onDone={done}
              trigger={<ActionRow icon={<FolderInput className="size-4" />} label="Move to another tab…" />}
            />

            <div className="flex flex-col gap-2">
              <RenamePaneDialog
                target={target}
                current={label}
                trigger={<ActionRow icon={<Pencil className="size-4" />} label="Rename pane" />}
              />
              <ConfirmAction
                operation="pane.close"
                resourceId={pane.id}
                label={label}
                params={{ pane_id: pane.id }}
                expectedGeneration={pane.generation}
                onDone={done}
                trigger={<ActionRow icon={<Trash2 className="size-4" />} label="Close pane" destructive />}
              />
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}

/** A direct console link for a pane, used where a full sheet is overkill. */
export function ConsoleLink({ pane, className }: { pane: Pane; className?: string }) {
  return (
    <Link
      to={`/console/${encodeURIComponent(pane.id)}?generation=${pane.generation}`}
      className={cn(
        "inline-flex min-h-11 items-center gap-1.5 px-2 text-meta text-muted-ink hover:text-mist",
        "focus-visible:outline-2 focus-visible:outline-brass",
        className,
      )}
    >
      <SquareTerminal className="size-4" aria-hidden />
      Console
    </Link>
  );
}
