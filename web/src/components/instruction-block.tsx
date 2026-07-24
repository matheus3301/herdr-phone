import { CircleAlert, CircleCheck, CircleDashed, CircleHelp } from "lucide-react";
import { Button } from "@/components/ui/button";
import { relativeTime } from "@/lib/format";
import { DELIVERY_LABEL, type Instruction } from "@/lib/run-store";
import { cn } from "@/lib/utils";

const MARK = {
  pending: CircleDashed,
  accepted: CircleCheck,
  delivery_unknown: CircleHelp,
  rejected: CircleAlert,
} as const;

const TONE = {
  pending: "text-muted-ink",
  accepted: "text-tide",
  delivery_unknown: "text-brass",
  rejected: "text-flare",
} as const;

/**
 * A user instruction: a compact tinted block, not a chat bubble.
 *
 * Delivery state is stated plainly. `delivery_unknown` is the honest outcome
 * when the relay lost certainty after Herdr may already have accepted the
 * prompt — the UI offers to send again as a *deliberate* act and never retries
 * on its own, because a duplicated instruction to a live shell is worse than an
 * unanswered one.
 */
export function InstructionBlock({
  instruction,
  now,
  onResend,
  onDismiss,
}: {
  instruction: Instruction;
  now: number;
  onResend: (text: string) => void;
  onDismiss: (id: string) => void;
}) {
  const Mark = MARK[instruction.state];
  const uncertain = instruction.state === "delivery_unknown";
  const rejected = instruction.state === "rejected";

  return (
    <div
      className={cn(
        "rounded-log border-l-2 bg-brass/8 px-3 py-2",
        uncertain ? "border-brass" : rejected ? "border-flare" : "border-brass/60",
      )}
    >
      <p className="text-meta font-semibold text-muted-ink">You</p>
      <p className="whitespace-pre-wrap break-words text-prose text-mist">{instruction.text}</p>
      <p className={cn("mt-1 flex flex-wrap items-center gap-1.5 text-meta", TONE[instruction.state])}>
        <Mark className="size-3.5 shrink-0" aria-hidden />
        {DELIVERY_LABEL[instruction.state]}
        <span className="tabular text-faint-ink">{relativeTime(instruction.createdAt, now)}</span>
      </p>
      {instruction.error && (
        <p className="mt-1 text-meta text-muted-ink" role={rejected ? "alert" : undefined}>
          {instruction.error}
        </p>
      )}
      {uncertain && (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <p className="w-full text-meta text-muted-ink">
            The agent may already have received this. Check the console before sending it again.
          </p>
          <Button size="sm" variant="outline" onClick={() => onResend(instruction.text)}>
            Send again
          </Button>
          <Button size="sm" variant="quiet" onClick={() => onDismiss(instruction.id)}>
            Leave it
          </Button>
        </div>
      )}
      {rejected && (
        <div className="mt-2">
          <Button size="sm" variant="quiet" onClick={() => onDismiss(instruction.id)}>
            Dismiss
          </Button>
        </div>
      )}
    </div>
  );
}
