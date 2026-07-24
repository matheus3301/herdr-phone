import { Activity, Circle, CircleDot, CircleHelp, OctagonAlert } from "lucide-react";
import { statusLabel } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { AgentStatus } from "@/lib/types";

/**
 * Status is carried by an icon, a word, and a colour together — never colour
 * alone, and never animation. "Updated" is deliberately paired with a neutral
 * mark: Herdr reports that background work settled, not that it succeeded.
 */
const ICON = {
  blocked: OctagonAlert,
  working: Activity,
  done: CircleDot,
  idle: Circle,
  unknown: CircleHelp,
} as const;

const TEXT: Record<AgentStatus, string> = {
  blocked: "text-flare",
  working: "text-tide",
  done: "text-brass",
  idle: "text-muted-ink",
  unknown: "text-muted-ink",
};

export function StatusPill({
  status,
  className,
  showLabel = true,
}: {
  status: AgentStatus;
  className?: string;
  showLabel?: boolean;
}) {
  const Icon = ICON[status];
  const label = statusLabel(status);
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-meta font-medium", TEXT[status], className)}>
      <Icon className="size-3.5 shrink-0" aria-hidden />
      {showLabel ? label : <span className="sr-only">{label}</span>}
    </span>
  );
}
