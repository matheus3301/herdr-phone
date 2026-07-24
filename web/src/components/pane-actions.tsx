import { useState, type ReactNode } from "react";
import {
  Columns2,
  Rows2,
  Maximize2,
  Minimize2,
  ArrowLeftRight,
  PanelRight,
  Pencil,
  Trash2,
  MoveRight,
  ChevronsLeftRight,
  ChevronsUpDown,
  Bot,
  FolderInput,
} from "lucide-react";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { RenameDialog } from "@/components/rename-dialog";
import { ConfirmAction } from "@/components/confirm-action";
import { StartAgentSheet } from "@/components/agent-actions";
import { useMutations } from "@/hooks/use-mutations";
import { useAppState } from "@/hooks/use-app-store";
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
        "flex min-h-11 w-full items-center gap-3 rounded-[10px] border border-frame bg-hull px-3 text-left text-sm",
        "hover:bg-bulkhead disabled:opacity-45 focus-visible:outline-2 focus-visible:outline-brass",
        destructive ? "text-flare" : "text-mist",
      )}
    >
      <span className={cn("shrink-0", destructive ? "text-flare" : "text-muted-ink")}>{icon}</span>
      {label}
    </button>
  );
}

/** Pick a destination tab for pane.move → existing tab (SPEC §15, M4). */
function MoveToTabSheet({ pane, gen, trigger, onDone }: { pane: Pane; gen: number; trigger: ReactNode; onDone?: () => void }) {
  const { snapshot } = useAppState();
  const { run } = useMutations();
  const [open, setOpen] = useState(false);
  const targets = (snapshot?.tabs ?? [])
    .filter((t) => t.workspaceId === pane.workspaceId && t.id !== pane.tabId)
    .sort((a, b) => a.order - b.order);

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby={undefined}>
        <SheetHeader>
          <SheetTitle>Move pane to tab</SheetTitle>
        </SheetHeader>
        {targets.length === 0 ? (
          <p className="py-2 text-sm text-muted-ink">No other tabs in this workspace.</p>
        ) : (
          <div className="flex flex-col gap-1.5">
            {targets.map((t) => (
              <button
                key={t.id}
                type="button"
                className="flex min-h-11 items-center gap-2 rounded-[10px] border border-frame bg-hull px-3 text-left text-sm text-mist hover:bg-bulkhead focus-visible:outline-2 focus-visible:outline-brass"
                onClick={async () => {
                  await run("pane.move", { pane_id: pane.id, destination: { type: "tab", tab_id: t.id } }, { expectedGeneration: gen });
                  setOpen(false);
                  onDone?.();
                }}
              >
                <FolderInput className="size-4 text-muted-ink" />
                <span className="flex-1 truncate">{t.label}</span>
                <span className="font-utility text-[11px] text-muted-ink">{t.id}</span>
              </button>
            ))}
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

/** Full pane action surface (SPEC §15 panes). Controlled so a completed nested
 * action (start agent, move to tab, close) dismisses the whole stack. */
export function PaneActions({ pane, trigger, onClose }: { pane: Pane; trigger: ReactNode; onClose?: () => void }) {
  const { run } = useMutations();
  const { snapshot } = useAppState();
  const [open, setOpen] = useState(false);
  const label = pane.title ?? pane.agentName ?? pane.id;
  const gen = pane.generation;
  const siblings = (snapshot?.panes ?? []).filter((p) => p.tabId === pane.tabId).sort((a, b) => a.order - b.order);
  const idx = siblings.findIndex((p) => p.id === pane.id);
  const swapTarget = siblings.length > 1 ? siblings[(idx + 1) % siblings.length] : null;
  const isShell = !pane.agentKind;
  const done = () => {
    setOpen(false);
    onClose?.();
  };

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent aria-describedby={undefined}>
        <SheetHeader>
          <SheetTitle>
            Pane <span className="font-utility text-sm text-muted-ink">{pane.id}</span>
          </SheetTitle>
        </SheetHeader>

        {isShell && (
          <StartAgentSheet
            paneId={pane.id}
            onDone={done}
            trigger={<ActionRow icon={<Bot className="size-4" />} label="Start agent" />}
          />
        )}

        <div className="grid grid-cols-2 gap-2">
          <ActionRow icon={<Columns2 className="size-4" />} label="Split right" onClick={() => run("pane.split", { pane_id: pane.id, direction: "right" }, { expectedGeneration: gen })} />
          <ActionRow icon={<Rows2 className="size-4" />} label="Split down" onClick={() => run("pane.split", { pane_id: pane.id, direction: "down" }, { expectedGeneration: gen })} />
          <ActionRow
            icon={pane.zoomed ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
            label={pane.zoomed ? "Unzoom" : "Zoom"}
            onClick={() => run("pane.zoom", { pane_id: pane.id }, { expectedGeneration: gen })}
          />
          <ActionRow
            icon={<ArrowLeftRight className="size-4" />}
            label="Swap"
            disabled={!swapTarget}
            onClick={() => swapTarget && run("pane.swap", { pane_id: pane.id, target_pane_id: swapTarget.id }, { expectedGeneration: gen })}
          />
          <ActionRow icon={<ChevronsLeftRight className="size-4" />} label="Widen" onClick={() => run("pane.resize", { pane_id: pane.id, direction: "right", amount: 4 }, { expectedGeneration: gen })} />
          <ActionRow icon={<ChevronsUpDown className="size-4" />} label="Taller" onClick={() => run("pane.resize", { pane_id: pane.id, direction: "down", amount: 4 }, { expectedGeneration: gen })} />
          <ActionRow icon={<PanelRight className="size-4" />} label="Move → new tab" onClick={() => run("pane.move", { pane_id: pane.id, destination: { type: "new_tab" } }, { expectedGeneration: gen })} />
          <ActionRow icon={<MoveRight className="size-4" />} label="Move → new space" onClick={() => run("pane.move", { pane_id: pane.id, destination: { type: "new_workspace" } }, { expectedGeneration: gen })} />
        </div>

        <MoveToTabSheet
          pane={pane}
          gen={gen}
          onDone={done}
          trigger={<ActionRow icon={<FolderInput className="size-4" />} label="Move → tab…" />}
        />

        <div className="mt-1 flex flex-col gap-2">
          <RenameDialog
            title="Rename pane"
            operation="pane.rename"
            idKey="pane_id"
            idValue={pane.id}
            valueKey="label"
            current={label}
            trigger={<ActionRow icon={<Pencil className="size-4" />} label="Rename pane" />}
          />
          <ConfirmAction
            operation="pane.close"
            resourceId={pane.id}
            label={label}
            params={{ pane_id: pane.id }}
            expectedGeneration={gen}
            onDone={done}
            trigger={<ActionRow icon={<Trash2 className="size-4" />} label="Close pane" destructive />}
          />
        </div>
      </SheetContent>
    </Sheet>
  );
}
