import { cn } from "@/lib/utils";
import { statusLabel, statusTone } from "@/lib/format";
import type { AgentStatus } from "@/lib/types";

const TONE_BG: Record<string, string> = {
  flare: "bg-flare",
  tide: "bg-tide",
  brass: "bg-brass",
  muted: "bg-muted-ink",
};

/** A small agent-status indicator with an accessible label. */
export function StatusDot({
  status,
  pulse = true,
  className,
}: {
  status: AgentStatus;
  pulse?: boolean;
  className?: string;
}) {
  const tone = statusTone(status);
  return (
    <span
      className={cn("inline-block size-2.5 shrink-0 rounded-full", TONE_BG[tone], status === "blocked" && pulse && "animate-beacon", className)}
      role="img"
      aria-label={statusLabel(status)}
      title={statusLabel(status)}
    />
  );
}
