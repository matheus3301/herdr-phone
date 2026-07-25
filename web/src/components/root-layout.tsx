import { Outlet, useLocation } from "react-router-dom";
import { AppBar } from "@/components/app-bar";
import { PrimaryNav } from "@/components/primary-nav";
import { ConnectionBanner } from "@/components/connection-banner";
import { RunInbox } from "@/components/run-inbox";
import { useRuns } from "@/hooks/use-runs";
import { useVisualViewport } from "@/hooks/use-visual-viewport";
import { keyboardOpen } from "@/lib/keyboard-layout";
import { attentionCount } from "@/lib/run";

/**
 * The app shell.
 *
 * The inbox and the detail column are both mounted for the whole session and
 * placed by CSS grid: stacked in one cell on a phone (one shown at a time),
 * side by side at desktop width. Nothing remounts when the viewport crosses the
 * breakpoint, so a live run — or an attached console — survives a rotation or a
 * window resize.
 */
export function RootLayout() {
  const location = useLocation();
  const runs = useRuns();
  const vp = useVisualViewport();

  // Everything except the inbox itself is a "detail" surface.
  const detail = location.pathname !== "/";
  const attention = attentionCount(runs);
  // While the software keyboard is up, the composer owns the space above it.
  const hideNav = keyboardOpen(vp);

  // The skip link must land on content that is actually on screen. Below the
  // wide breakpoint the two columns share one grid cell and only one is
  // displayed, so at `/` the visible content is the inbox and `#main` is
  // `display:none` — skipping to it moved focus nowhere and left the next Tab in
  // the bottom nav, past everything. Both regions carry `tabIndex={-1}` so
  // activating the link really does move `document.activeElement`, rather than
  // relying on the browser's sequential-focus-start fallback.
  const skipTarget = detail ? "#main" : "#inbox";

  return (
    <div className="app-shell bg-deck" data-detail={detail}>
      <a className="skip-link" href={skipTarget}>
        Skip to content
      </a>
      <AppBar attention={attention} />
      <div className="shell-banner">
        <ConnectionBanner />
      </div>

      <aside id="inbox" tabIndex={-1} className="shell-inbox flex flex-col" aria-label="Agent runs">
        <RunInbox />
      </aside>

      <main id="main" tabIndex={-1} className="shell-main flex flex-col">
        <Outlet />
      </main>

      {!hideNav && <PrimaryNav attention={attention} />}
    </div>
  );
}
