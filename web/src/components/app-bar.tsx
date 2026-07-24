import { useNavigate } from "react-router-dom";
import { Settings } from "lucide-react";
import { BrandMark } from "@/components/brand-mark";
import { Button } from "@/components/ui/button";
import { useAppState } from "@/hooks/use-app-store";
import { cn } from "@/lib/utils";
import type { ConnectionState } from "@/lib/store";

const LINK_TEXT: Record<ConnectionState, string> = {
  connecting: "Connecting",
  live: "Connected",
  trouble: "Reconnecting",
  lost: "Disconnected",
};

const LINK_DOT: Record<ConnectionState, string> = {
  connecting: "bg-muted-ink",
  live: "bg-tide",
  trouble: "bg-brass",
  lost: "bg-flare",
};

/** Identity, link state, and the way out to Settings. Deliberately thin. */
export function AppBar({ attention }: { attention: number }) {
  const { connection } = useAppState();
  const navigate = useNavigate();

  return (
    <header className="shell-bar flex items-center gap-2 border-b border-seam bg-deck px-3 pb-2 pt-[calc(8px+var(--spacing-safe-top))] lg:pt-2">
      <BrandMark className="size-5" beacon={attention > 0} />
      <span className="text-body font-semibold text-mist">Herdr</span>
      <span className="flex items-center gap-1.5 text-meta text-muted-ink" role="status" aria-live="polite">
        <span className={cn("size-1.5 rounded-full", LINK_DOT[connection])} aria-hidden />
        {LINK_TEXT[connection]}
      </span>
      <Button
        variant="quiet"
        size="icon"
        className="ml-auto"
        aria-label="Settings"
        onClick={() => navigate("/settings")}
      >
        <Settings className="size-5" />
      </Button>
    </header>
  );
}
