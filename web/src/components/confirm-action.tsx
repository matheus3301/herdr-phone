import { useState, type ReactNode } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { useMutations } from "@/hooks/use-mutations";
import { fallbackSummary } from "@/lib/confirm";
import type { MutationOperation } from "@/lib/types";

interface ConfirmActionProps {
  operation: MutationOperation;
  resourceId: string;
  label: string;
  params: Record<string, unknown>;
  expectedGeneration?: number;
  /** Optional escalation (e.g. worktree.remove → worktree.remove_force) offered
   * when the primary operation is refused by the backend. */
  escalateOperation?: MutationOperation;
  trigger: ReactNode;
  onDone?: () => void;
}

/**
 * Structural destructive action guarded by a shadcn AlertDialog + a single-use
 * server confirmation nonce (SPEC §14.4, §15). The backend returns no summary, so
 * the copy is client-derived. When the primary op is refused and an escalation is
 * configured, the dialog advances to a distinct forced-operation confirmation.
 */
export function ConfirmAction({
  operation,
  resourceId,
  label,
  params,
  expectedGeneration,
  escalateOperation,
  trigger,
  onDone,
}: ConfirmActionProps) {
  const { run, prepareConfirmation, pending } = useMutations();
  const [open, setOpen] = useState(false);
  const [phase, setPhase] = useState<"primary" | "escalated">("primary");
  const [confirmation, setConfirmation] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loadingNonce, setLoadingNonce] = useState(false);

  const currentOp: MutationOperation = phase === "escalated" && escalateOperation ? escalateOperation : operation;

  async function prepareFor(op: MutationOperation) {
    setLoadingNonce(true);
    setError(null);
    try {
      const c = await prepareConfirmation(op, resourceId, params, expectedGeneration);
      setConfirmation(c.confirmation);
    } catch {
      setConfirmation(null);
      setError("Could not prepare confirmation.");
    } finally {
      setLoadingNonce(false);
    }
  }

  async function onOpenChange(next: boolean) {
    setOpen(next);
    if (next) {
      setPhase("primary");
      await prepareFor(operation);
    }
  }

  async function confirm() {
    const res = await run(currentOp, params, {
      confirmation: confirmation ?? undefined,
      expectedGeneration,
    });
    if (res && !("error" in res && res.error)) {
      setOpen(false);
      onDone?.();
      return;
    }
    const message = res && "error" in res && res.error ? res.error.message : "Action failed.";
    // Offer the escalated (forced) operation once, if configured.
    if (escalateOperation && phase === "primary") {
      setPhase("escalated");
      setError(message);
      await prepareFor(escalateOperation);
      return;
    }
    setError(message);
  }

  const escalated = phase === "escalated";

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{escalated ? `Force remove "${label}"?` : `Confirm: ${label}`}</AlertDialogTitle>
          <AlertDialogDescription>
            {loadingNonce ? "Preparing…" : fallbackSummary(currentOp, label)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error && (
          <p className="text-[13px] text-flare" role="alert">
            {error}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={(e) => {
              e.preventDefault();
              void confirm();
            }}
            disabled={pending || loadingNonce}
          >
            {pending ? "Working…" : escalated ? "Force remove" : "Confirm"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
