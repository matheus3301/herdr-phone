import { useRef, useState } from "react";
import { SendHorizontal, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { assessDanger } from "@/lib/danger";
import { cn } from "@/lib/utils";
import type { Run } from "@/lib/run";

export type ComposerResult = "accepted" | "uncertain" | "kept";

/**
 * The run composer.
 *
 * Three rules it exists to enforce:
 *
 *  1. The exact target is visible before sending. This drives a real shell on a
 *     real machine; "which agent am I talking to" is never implicit.
 *  2. The draft is never destroyed by a failure. It clears only when the relay
 *     accepted the instruction, or when delivery became genuinely uncertain and
 *     the instruction is now tracked in the run with its own recovery. A
 *     rejection or a dead link leaves the text exactly where the operator left it.
 *  3. Enter sends, Shift+Enter is a newline — but never while an IME
 *     composition is open, where Enter commits a candidate.
 */
export function RunComposer({
  run,
  value,
  onChange,
  onSubmit,
  pending,
  disabled = false,
  disabledReason,
}: {
  run: Run;
  value: string;
  onChange: (next: string) => void;
  onSubmit: (text: string) => Promise<ComposerResult>;
  pending: boolean;
  disabled?: boolean;
  disabledReason?: string;
}) {
  const [armed, setArmed] = useState(false);
  const composing = useRef(false);
  const field = useRef<HTMLTextAreaElement | null>(null);
  const danger = assessDanger(value);

  async function submit() {
    if (disabled || pending || !value.trim()) return;
    // Advisory only: an authorized shell is an authorized shell. The second tap
    // is a speed bump against a mis-tap, never a sandbox.
    if (danger.danger && !armed) {
      setArmed(true);
      return;
    }
    const result = await onSubmit(value);
    if (result === "accepted" || result === "uncertain") {
      onChange("");
      setArmed(false);
    }
    field.current?.focus();
  }

  return (
    <div className="border-t border-seam bg-bulkhead px-3 pb-[calc(10px+var(--spacing-safe-bottom))] pt-2">
      <div className="mx-auto w-full max-w-[46rem]">
        <p className="mb-1.5 flex items-center gap-1.5 text-meta text-muted-ink">
          <span className="text-faint-ink">To</span>
          <span className="tabular truncate text-mist">
            {run.agentName} · {run.workspaceLabel}
            {run.worktree && run.worktree.label !== run.workspaceLabel ? ` / ${run.worktree.label}` : ""}
          </span>
        </p>

        {disabled && disabledReason && (
          <p className="mb-1.5 text-meta text-flare" role="status">
            {disabledReason}
          </p>
        )}

        {danger.danger && (
          <p className="mb-1.5 flex items-center gap-1.5 text-meta text-flare" role="status" aria-live="polite">
            <TriangleAlert className="size-3.5 shrink-0" aria-hidden />
            {danger.reason}. Send again to confirm.
          </p>
        )}

        <div className="flex items-end gap-2">
          <Textarea
            ref={field}
            value={value}
            disabled={disabled}
            aria-label={`Instruction for ${run.agentName}`}
            placeholder="Add an instruction…"
            autoCapitalize="sentences"
            autoCorrect="on"
            spellCheck
            onChange={(e) => {
              onChange(e.target.value);
              setArmed(false);
            }}
            onCompositionStart={() => {
              composing.current = true;
            }}
            onCompositionEnd={() => {
              composing.current = false;
            }}
            onKeyDown={(e) => {
              if (e.key !== "Enter" || e.shiftKey) return;
              // `isComposing` covers browsers that report it; the ref covers the
              // rest. Either way Enter must not steal an IME candidate commit.
              if (composing.current || e.nativeEvent.isComposing) return;
              e.preventDefault();
              void submit();
            }}
            className={cn(danger.danger && "ring-flare/60")}
          />
          <Button
            variant={danger.danger && armed ? "danger" : "primary"}
            size="icon"
            // `shrink-0` is load-bearing, not decoration: the textarea beside it is
            // `w-full`, so without it flex shrinks the primary action of the run
            // below the 44px minimum target (measured 38.08px on a 390px phone).
            className="shrink-0"
            onClick={() => void submit()}
            disabled={disabled || pending || !value.trim()}
            aria-label={danger.danger && armed ? "Confirm and send instruction" : "Send instruction"}
          >
            <SendHorizontal className="size-5" />
          </Button>
        </div>
      </div>
    </div>
  );
}
