import { useMemo, useRef, useState, type ReactNode, type TouchEvent as ReactTouchEvent } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronDown, ChevronLeft, ChevronRight, Plus, Pencil, Trash2, Ellipsis } from "lucide-react";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import { StatusDot } from "@/components/status-dot";
import { CreateWorkspaceSheet, CreateTabSheet } from "@/components/create-sheets";
import { RenameDialog } from "@/components/rename-dialog";
import { ConfirmAction } from "@/components/confirm-action";
import { PaneActions } from "@/components/pane-actions";
import { useAppState } from "@/hooks/use-app-store";
import { useSelectedPaneId } from "@/hooks/use-selection";
import { useMutations } from "@/hooks/use-mutations";
import { selectionStore, resolveSelection } from "@/lib/selection";
import { aggregateStatus } from "@/lib/triage";
import { cn } from "@/lib/utils";
import type { AgentStatus, Pane, Snapshot, Tab, Workspace } from "@/lib/types";

function paneStatus(pane: Pane): AgentStatus {
  return pane.agentStatus ?? "unknown";
}
function tabStatus(snapshot: Snapshot, tab: Tab): AgentStatus {
  return aggregateStatus(snapshot.panes.filter((p) => p.tabId === tab.id).map(paneStatus));
}

/** A single scrollable layer of the ribbon. */
function Layer({
  levelLabel,
  onOpenSwitcher,
  children,
  indent,
  onSwipe,
}: {
  levelLabel: string;
  onOpenSwitcher: () => void;
  children: ReactNode;
  indent: number;
  /** Change the active sibling at this level (SPEC §14.2). Only invoked when the
   * layer isn't horizontally scrollable, so native chip scrolling is never
   * hijacked. dir: -1 = previous, +1 = next. */
  onSwipe?: (dir: -1 | 1) => void;
}) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const start = useRef<{ x: number; y: number } | null>(null);

  function onTouchStart(e: ReactTouchEvent) {
    const t = e.touches[0];
    start.current = { x: t.clientX, y: t.clientY };
  }
  function onTouchEnd(e: ReactTouchEvent) {
    const s = start.current;
    start.current = null;
    if (!s || !onSwipe) return;
    const el = scrollRef.current;
    // If the chip strip can scroll horizontally, the gesture is a scroll — leave it.
    if (el && el.scrollWidth > el.clientWidth + 4) return;
    const t = e.changedTouches[0];
    const dx = t.clientX - s.x;
    const dy = t.clientY - s.y;
    if (Math.abs(dx) < 48 || Math.abs(dx) <= Math.abs(dy)) return;
    onSwipe(dx < 0 ? 1 : -1); // swipe left → next, swipe right → previous
  }

  return (
    <div className={cn("flex items-stretch", indent > 0 && "border-l border-seam")} style={{ paddingLeft: indent }}>
      <button
        type="button"
        onClick={onOpenSwitcher}
        className="flex shrink-0 items-center gap-1 rounded-md px-2 py-1 font-utility text-[10px] uppercase tracking-wider text-muted-ink hover:text-mist focus-visible:outline-2 focus-visible:outline-brass"
        aria-label={`Open ${levelLabel} switcher`}
      >
        {levelLabel}
        <ChevronDown className="size-3" />
      </button>
      <div
        ref={scrollRef}
        onTouchStart={onTouchStart}
        onTouchEnd={onTouchEnd}
        className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto pb-0.5 [scrollbar-width:none]"
      >
        {children}
      </div>
    </div>
  );
}

function Chip({
  active,
  status,
  label,
  onClick,
}: {
  active: boolean;
  status?: AgentStatus;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? "true" : undefined}
      className={cn(
        "flex h-8 shrink-0 items-center gap-1.5 rounded-[8px] border px-2.5 text-[13px]",
        "focus-visible:outline-2 focus-visible:outline-brass",
        active
          ? "border-brass bg-brass/15 text-mist"
          : "border-frame bg-bulkhead text-muted-ink hover:text-mist",
      )}
    >
      {status && <StatusDot status={status} pulse={false} className="size-2" />}
      <span className="max-w-[10rem] truncate">{label}</span>
    </button>
  );
}

export function TopologyRibbon({ orientation = "horizontal" }: { orientation?: "horizontal" | "vertical" }) {
  const { snapshot } = useAppState();
  const selectedPaneId = useSelectedPaneId();
  const { run } = useMutations();
  const navigate = useNavigate();

  const sel = useMemo(() => resolveSelection(snapshot, selectedPaneId), [snapshot, selectedPaneId]);
  const [openLevel, setOpenLevel] = useState<null | "workspace" | "tab" | "pane">(null);

  const workspaces = snapshot?.workspaces ?? [];
  const tabs = useMemo(
    () => (snapshot && sel.workspace ? snapshot.tabs.filter((t) => t.workspaceId === sel.workspace!.id).sort((a, b) => a.order - b.order) : []),
    [snapshot, sel.workspace],
  );
  const panes = useMemo(
    () => (snapshot && sel.tab ? snapshot.panes.filter((p) => p.tabId === sel.tab!.id).sort((a, b) => a.order - b.order) : []),
    [snapshot, sel.tab],
  );

  function firstPaneOfTab(tabId: string): Pane | undefined {
    return snapshot?.panes.filter((p) => p.tabId === tabId).sort((a, b) => a.order - b.order)[0];
  }
  function firstPaneOfWorkspace(ws: Workspace): Pane | undefined {
    const tab = snapshot?.tabs.find((t) => t.id === ws.activeTabId) ?? snapshot?.tabs.find((t) => t.workspaceId === ws.id);
    return tab ? firstPaneOfTab(tab.id) : undefined;
  }

  function openPane(paneId: string, extra?: { tabId?: string; workspaceId?: string }) {
    selectionStore.set(paneId);
    if (extra?.workspaceId) void run("workspace.focus", { workspace_id: extra.workspaceId });
    if (extra?.tabId) void run("tab.focus", { tab_id: extra.tabId });
    void run("pane.focus", { pane_id: paneId });
    navigate("/");
  }

  // Swipe a layer (when it isn't scrollable) to step to the previous/next sibling.
  function stepWorkspace(dir: -1 | 1) {
    const i = workspaces.findIndex((w) => w.id === sel.workspace?.id);
    const next = workspaces[i + dir];
    if (!next) return;
    const p = firstPaneOfWorkspace(next);
    if (p) openPane(p.id, { workspaceId: next.id, tabId: p.tabId });
  }
  function stepTab(dir: -1 | 1) {
    const i = tabs.findIndex((t) => t.id === sel.tab?.id);
    const next = tabs[i + dir];
    if (!next) return;
    const p = firstPaneOfTab(next.id);
    if (p) openPane(p.id, { tabId: next.id });
  }
  function stepPane(dir: -1 | 1) {
    const i = panes.findIndex((p) => p.id === sel.pane?.id);
    const next = panes[i + dir];
    if (next) openPane(next.id);
  }

  if (!snapshot || !sel.workspace) {
    return (
      <div className="px-3 py-2 font-utility text-[12px] text-muted-ink">No workspaces yet.</div>
    );
  }

  const vertical = orientation === "vertical";

  return (
    <nav
      aria-label="Topology"
      className={cn(
        "flex flex-col gap-1 bg-deck",
        vertical ? "p-2" : "px-2 pt-1 pb-1.5",
      )}
    >
      <Layer levelLabel="Space" indent={0} onOpenSwitcher={() => setOpenLevel("workspace")} onSwipe={stepWorkspace}>
        {workspaces.map((w) => (
          <Chip
            key={w.id}
            active={w.id === sel.workspace!.id}
            status={w.agentStatus}
            label={w.label}
            onClick={() => {
              const p = firstPaneOfWorkspace(w);
              if (p) openPane(p.id, { workspaceId: w.id, tabId: p.tabId });
            }}
          />
        ))}
      </Layer>

      <Layer levelLabel="Tab" indent={12} onOpenSwitcher={() => setOpenLevel("tab")} onSwipe={stepTab}>
        {tabs.map((t) => (
          <Chip
            key={t.id}
            active={t.id === sel.tab?.id}
            status={tabStatus(snapshot, t)}
            label={t.label}
            onClick={() => {
              const p = firstPaneOfTab(t.id);
              if (p) openPane(p.id, { tabId: t.id });
            }}
          />
        ))}
      </Layer>

      <Layer levelLabel="Pane" indent={24} onOpenSwitcher={() => setOpenLevel("pane")} onSwipe={stepPane}>
        {panes.map((p) => (
          <Chip
            key={p.id}
            active={p.id === sel.pane?.id}
            status={paneStatus(p)}
            label={p.agentName ? `${p.agentName}` : (p.title ?? p.id)}
            onClick={() => openPane(p.id)}
          />
        ))}
      </Layer>

      {/* Workspace switcher */}
      <Sheet open={openLevel === "workspace"} onOpenChange={(o) => setOpenLevel(o ? "workspace" : null)}>
        <SheetContent aria-describedby={undefined}>
          <SheetHeader>
            <SheetTitle>Workspaces</SheetTitle>
          </SheetHeader>
          <div className="flex flex-col gap-1.5">
            {workspaces.map((w) => (
              <div key={w.id} className="flex items-center gap-1.5 rounded-[10px] border border-frame bg-hull pr-1.5">
                <button
                  type="button"
                  onClick={() => {
                    const p = firstPaneOfWorkspace(w);
                    if (p) openPane(p.id, { workspaceId: w.id, tabId: p.tabId });
                    setOpenLevel(null);
                  }}
                  className="flex min-h-11 flex-1 items-center gap-2 px-3 text-left focus-visible:outline-2 focus-visible:outline-brass"
                >
                  <StatusDot status={w.agentStatus} />
                  <span className="flex-1 truncate text-sm text-mist">{w.label}</span>
                  <span className="font-utility text-[11px] text-muted-ink">{w.paneCount} panes</span>
                </button>
                <RenameDialog
                  title="Rename workspace"
                  operation="workspace.rename"
                  idKey="workspace_id"
                  idValue={w.id}
                  valueKey="label"
                  current={w.label}
                  trigger={
                    <Button variant="ghost" size="icon" aria-label={`Rename ${w.label}`}>
                      <Pencil className="size-4" />
                    </Button>
                  }
                />
                <ConfirmAction
                  operation="workspace.close"
                  resourceId={w.id}
                  label={w.label}
                  params={{ workspace_id: w.id }}
                  trigger={
                    <Button variant="ghost" size="icon" aria-label={`Close ${w.label}`}>
                      <Trash2 className="size-4 text-flare" />
                    </Button>
                  }
                />
              </div>
            ))}
            <CreateWorkspaceSheet
              trigger={
                <Button variant="outline" className="mt-1 w-full justify-center gap-2">
                  <Plus className="size-4" /> New workspace
                </Button>
              }
            />
          </div>
        </SheetContent>
      </Sheet>

      {/* Tab switcher */}
      <Sheet open={openLevel === "tab"} onOpenChange={(o) => setOpenLevel(o ? "tab" : null)}>
        <SheetContent aria-describedby={undefined}>
          <SheetHeader>
            <SheetTitle>Tabs · {sel.workspace.label}</SheetTitle>
          </SheetHeader>
          <div className="flex flex-col gap-1.5">
            {tabs.map((t, i) => (
              <div key={t.id} className="flex items-center gap-1.5 rounded-[10px] border border-frame bg-hull pr-1.5">
                <button
                  type="button"
                  onClick={() => {
                    const p = firstPaneOfTab(t.id);
                    if (p) openPane(p.id, { tabId: t.id });
                    setOpenLevel(null);
                  }}
                  className="flex min-h-11 flex-1 items-center gap-2 px-3 text-left focus-visible:outline-2 focus-visible:outline-brass"
                >
                  <StatusDot status={tabStatus(snapshot, t)} />
                  <span className="flex-1 truncate text-sm text-mist">{t.label}</span>
                  <span className="font-utility text-[11px] text-muted-ink">{t.paneCount} panes</span>
                </button>
                {/* Reorder (tab.move). insert_index counts the pre-removal list, so
                    moving toward the end needs target+1 (see HERDR_API). */}
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Move ${t.label} left`}
                  disabled={i === 0}
                  onClick={() => run("tab.move", { tab_id: t.id, insert_index: i - 1 })}
                >
                  <ChevronLeft className="size-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Move ${t.label} right`}
                  disabled={i === tabs.length - 1}
                  onClick={() => run("tab.move", { tab_id: t.id, insert_index: i + 2 })}
                >
                  <ChevronRight className="size-4" />
                </Button>
                <RenameDialog
                  title="Rename tab"
                  operation="tab.rename"
                  idKey="tab_id"
                  idValue={t.id}
                  valueKey="label"
                  current={t.label}
                  trigger={
                    <Button variant="ghost" size="icon" aria-label={`Rename ${t.label}`}>
                      <Pencil className="size-4" />
                    </Button>
                  }
                />
                <ConfirmAction
                  operation="tab.close"
                  resourceId={t.id}
                  label={t.label}
                  params={{ tab_id: t.id }}
                  trigger={
                    <Button variant="ghost" size="icon" aria-label={`Close ${t.label}`}>
                      <Trash2 className="size-4 text-flare" />
                    </Button>
                  }
                />
              </div>
            ))}
            <CreateTabSheet
              workspaceId={sel.workspace.id}
              trigger={
                <Button variant="outline" className="mt-1 w-full justify-center gap-2">
                  <Plus className="size-4" /> New tab
                </Button>
              }
            />
          </div>
        </SheetContent>
      </Sheet>

      {/* Pane switcher */}
      <Sheet open={openLevel === "pane"} onOpenChange={(o) => setOpenLevel(o ? "pane" : null)}>
        <SheetContent aria-describedby={undefined}>
          <SheetHeader>
            <SheetTitle>Panes · {sel.tab?.label}</SheetTitle>
          </SheetHeader>
          <div className="flex flex-col gap-1.5">
            {panes.map((p) => (
              <div key={p.id} className="flex items-center gap-1.5 rounded-[10px] border border-frame bg-hull pr-1.5">
                <button
                  type="button"
                  onClick={() => {
                    openPane(p.id);
                    setOpenLevel(null);
                  }}
                  className="flex min-h-11 flex-1 items-center gap-2 px-3 text-left focus-visible:outline-2 focus-visible:outline-brass"
                >
                  <StatusDot status={paneStatus(p)} />
                  <span className="flex-1 truncate text-sm text-mist">
                    {p.agentName ?? p.title ?? p.id}
                  </span>
                  <span className="font-utility text-[11px] text-muted-ink">{p.id}</span>
                </button>
                <PaneActions
                  pane={p}
                  onClose={() => setOpenLevel(null)}
                  trigger={
                    <Button variant="ghost" size="icon" aria-label={`Actions for ${p.id}`}>
                      <Ellipsis className="size-4" />
                    </Button>
                  }
                />
              </div>
            ))}
          </div>
        </SheetContent>
      </Sheet>
    </nav>
  );
}
