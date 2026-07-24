import { useMemo, useRef } from "react";
import { Ellipsis } from "lucide-react";
import { TerminalView, type TerminalHandle } from "@/components/terminal-view";
import { KeyDock } from "@/components/key-dock";
import { Composer } from "@/components/composer";
import { PaneActions } from "@/components/pane-actions";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { EmptyState, OfflineState } from "@/components/states";
import { CreateWorkspaceSheet } from "@/components/create-sheets";
import { useAppState } from "@/hooks/use-app-store";
import { useSelectedPaneId } from "@/hooks/use-selection";
import { usePrefs } from "@/hooks/use-prefs";
import { useVisualViewport } from "@/hooks/use-visual-viewport";
import { store } from "@/lib/store";
import { resolveSelection } from "@/lib/selection";
import { shortPath, statusLabel } from "@/lib/format";

export function TerminalRoute() {
  const { snapshot, connection } = useAppState();
  const selectedPaneId = useSelectedPaneId();
  const prefs = usePrefs();
  const vp = useVisualViewport();
  const termRef = useRef<TerminalHandle | null>(null);

  const sel = useMemo(() => resolveSelection(snapshot, selectedPaneId), [snapshot, selectedPaneId]);

  if (!snapshot) {
    if (connection === "lost") {
      return (
        <OfflineState
          title="Can't reach the relay"
          description="The connection to your Mac dropped. Check the tunnel, then retry."
          action={{ label: "Retry", onClick: () => store.revalidate() }}
        />
      );
    }
    return null; // boot splash / banner covers this transient window
  }

  if (!sel.pane) {
    return (
      <div className="flex h-full items-center justify-center">
        <EmptyState
          title="No panes yet"
          description="Create a workspace to open its first shell pane, then attach a terminal."
        />
        <div className="sr-only">
          <CreateWorkspaceSheet trigger={<button>New workspace</button>} />
        </div>
      </div>
    );
  }

  const pane = sel.pane;
  const send = (text: string) => termRef.current?.sendText(text);
  const chord = (c: string) => termRef.current?.sendChord(c);

  // Takeover requires a scoped terminal.takeover confirmation nonce (SPEC §13).
  // Guarded by canMutate() (a CSRF token must be present).
  const requestTakeover = async (): Promise<string | null> => {
    if (!store.canMutate()) return null;
    try {
      const c = await store.prepareConfirmation({
        operation: "terminal.takeover",
        resource_id: pane.id,
        expected_generation: pane.generation,
        params: {},
      });
      return c.confirmation;
    } catch {
      return null;
    }
  };

  return (
    <div
      className="flex h-full min-h-0 flex-col"
      // Lift the control shelf above the software keyboard (SPEC §14.4).
      style={{ paddingBottom: vp.keyboardInset }}
    >
      <div className="flex items-center gap-2 border-b border-seam bg-deck px-3 py-1.5">
        <StatusDot status={pane.agentStatus ?? "unknown"} />
        <span className="truncate text-sm text-mist">
          {pane.agentName ?? pane.title ?? "shell"}
        </span>
        {pane.agentStatus && (
          <span className="font-utility text-[11px] uppercase text-muted-ink">{statusLabel(pane.agentStatus)}</span>
        )}
        <span className="ml-auto truncate font-utility text-[11px] text-muted-ink" title={pane.cwd}>
          {shortPath(pane.cwd)}
        </span>
        <PaneActions
          pane={pane}
          trigger={
            <Button variant="ghost" size="icon" aria-label="Pane actions">
              <Ellipsis className="size-5" />
            </Button>
          }
        />
      </div>

      <div className="min-h-0 flex-1">
        <TerminalView
          key={pane.id}
          ref={termRef}
          paneId={pane.id}
          generation={pane.generation}
          fontSize={prefs.terminalFontSize}
          onRequestTakeover={requestTakeover}
        />
      </div>

      <KeyDock onChord={chord} onPaste={(text) => termRef.current?.paste(text)} />
      <Composer onSubmit={(text) => send(text + "\r")} placeholder={pane.agentName ? `reply to ${pane.agentName}…` : "message / command…"} />
    </div>
  );
}
