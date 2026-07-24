import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Settings } from "lucide-react";
import { BrandMark } from "@/components/brand-mark";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useAppState } from "@/hooks/use-app-store";
import { needsYouCount } from "@/lib/triage";
import { cn } from "@/lib/utils";
import type { ConnectionState } from "@/lib/store";

const CONN_TEXT: Record<ConnectionState, string> = {
  connecting: "connecting",
  live: "online",
  trouble: "reconnecting",
  lost: "offline",
};
const CONN_TONE: Record<ConnectionState, string> = {
  connecting: "text-muted-ink",
  live: "text-tide",
  trouble: "text-brass",
  lost: "text-flare",
};

export function AppHeader({ compact = false }: { compact?: boolean }) {
  const { snapshot, connection } = useAppState();
  const navigate = useNavigate();
  const needsYou = useMemo(() => (snapshot ? needsYouCount(snapshot.agents) : 0), [snapshot]);

  return (
    <header
      className={cn(
        "flex items-center gap-2 border-b border-frame bg-deck px-3",
        compact ? "py-2" : "pt-[calc(8px+var(--spacing-safe-top))] pb-2",
      )}
    >
      <BrandMark className="size-6" beacon={needsYou > 0} />
      <span className="font-utility text-[13px] font-medium uppercase tracking-widest text-mist">Herdr Phone</span>
      <span
        className={cn("ml-1 flex items-center gap-1 font-utility text-[11px] uppercase", CONN_TONE[connection])}
        role="status"
        aria-live="polite"
      >
        <span
          className={cn(
            "size-1.5 rounded-full",
            connection === "live" ? "bg-tide" : connection === "trouble" ? "bg-brass" : connection === "lost" ? "bg-flare" : "bg-muted-ink",
          )}
          aria-hidden
        />
        {CONN_TEXT[connection]}
      </span>
      <div className="ml-auto flex items-center gap-2">
        {needsYou > 0 && (
          <Badge tone="flare" aria-label={`${needsYou} agents need you`}>
            {needsYou} !
          </Badge>
        )}
        <Button variant="ghost" size="icon" aria-label="Settings" onClick={() => navigate("/settings")}>
          <Settings className="size-5" />
        </Button>
      </div>
    </header>
  );
}
