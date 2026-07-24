import { Outlet, useLocation } from "react-router-dom";
import { AppHeader } from "@/components/app-header";
import { BottomNav } from "@/components/bottom-nav";
import { TopologyRibbon } from "@/components/topology-ribbon";
import { ConnectionBanner } from "@/components/connection-banner";
import { useIsWide } from "@/hooks/use-media-query";
import { useVisualViewport } from "@/hooks/use-visual-viewport";
import { keyboardOpen } from "@/lib/keyboard-layout";

/**
 * App shell (SPEC §14.3). Narrow: header + topology ribbon + content + bottom
 * nav. Wide (>=768px): the ribbon becomes a left rail with the same content and
 * control shelf — one product, not a separate desktop app.
 */
export function RootLayout() {
  const wide = useIsWide();
  const vp = useVisualViewport();
  const location = useLocation();
  const onTerminal = location.pathname === "/" || location.pathname === "/terminal";
  const kbOpen = keyboardOpen(vp);

  if (wide) {
    return (
      <div className="grid h-dvh grid-cols-[300px_1fr] overflow-hidden bg-deck">
        <aside className="flex min-h-0 flex-col border-r border-frame bg-deck">
          <AppHeader compact />
          <div className="min-h-0 flex-1 overflow-y-auto">
            <TopologyRibbon orientation="vertical" />
          </div>
          <BottomNav orientation="vertical" />
        </aside>
        <div className="flex min-h-0 min-w-0 flex-col">
          <ConnectionBanner />
          <main className="min-h-0 min-w-0 flex-1 overflow-hidden">
            <Outlet />
          </main>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-deck">
      <AppHeader />
      <TopologyRibbon />
      <ConnectionBanner />
      <main className="min-h-0 flex-1 overflow-hidden">
        <Outlet />
      </main>
      {/* Hide the bottom nav while the keyboard is up on the terminal so the
          control shelf owns the space above the keyboard (SPEC §14.4). */}
      {!(onTerminal && kbOpen) && <BottomNav />}
    </div>
  );
}
