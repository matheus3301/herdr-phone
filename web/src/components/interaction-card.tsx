import { useState } from "react";
import { Link } from "react-router-dom";
import { CircleHelp, ShieldQuestion, SquareTerminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { InterpretedNotice } from "@/components/interpreted-notice";
import { useMutations } from "@/hooks/use-mutations";
import { validSendKey, type InterpretedInteraction, type InterpretedOption } from "@/lib/interpreted";
import type { Run } from "@/lib/run";

/**
 * The prompt the agent appears to be waiting on (SPEC §12.2).
 *
 * Answering is deliberately a two-step, never a one-tap:
 *
 *  1. Tapping an option opens a sheet that shows the **exact literal key** that
 *     will be delivered, quoted, plus which option it corresponds to.
 *  2. Only an explicit confirm sends it, through the existing `agent.send_keys`
 *     allowlist entry with the mandatory `expected_generation` guard.
 *
 * That is what keeps SPEC §21's "no blind one-tap approvals" true while still
 * making the prompt answerable from a phone: the detection may be a guess, but
 * what gets sent is shown before it is sent, and a person approves it.
 *
 * When `answerable` is false the options are rendered as plain text with no tap
 * targets at all, and the operator is routed to the console. That is the OpenCode
 * case: its selection row's highlight is carried by ANSI styling that the relay's
 * text read discards, so no key could be chosen without guessing.
 */
export function InteractionCard({ run, interaction }: { run: Run; interaction: InterpretedInteraction }) {
  const [pendingOption, setPendingOption] = useState<InterpretedOption | null>(null);

  const isApproval = interaction.kind === "approval";
  const Icon = isApproval ? ShieldQuestion : CircleHelp;

  return (
    <section
      aria-labelledby="interaction-heading"
      className="rounded-log bg-hull ring-1 ring-brass/40"
    >
      <div className="flex items-start gap-2 px-3 pt-3">
        <Icon className="mt-0.5 size-4 shrink-0 text-brass" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <h2 id="interaction-heading" className="text-body font-semibold text-mist">
            {isApproval ? "Looks like it needs permission" : "Looks like it's asking you something"}
          </h2>
          {interaction.title && <p className="mt-0.5 break-words text-body text-mist">{interaction.title}</p>}
        </div>
      </div>

      <div className="px-3 pb-3 pt-2">
        <InterpretedNotice parser={interaction.parser} className="mb-3 max-w-prose" />

        {interaction.detail.length > 0 && (
          <pre className="mb-3 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-log bg-terminal p-3 font-mono text-[12.5px] leading-relaxed text-terminal-ink">
            {interaction.detail.join("\n")}
          </pre>
        )}

        {interaction.diff.length > 0 && (
          <div className="mb-3 max-h-48 overflow-auto rounded-log bg-terminal p-2 font-mono text-[12.5px] leading-relaxed">
            {interaction.diff.map((line, i) => (
              <div
                key={`${i}-${line.line ?? "x"}`}
                className={
                  line.op === "add"
                    ? "text-tide"
                    : line.op === "remove"
                      ? "text-flare"
                      : "text-muted-ink"
                }
              >
                <span className="tabular mr-2 select-none text-faint-ink">{line.line ?? ""}</span>
                <span className="select-none">{line.op === "add" ? "+" : line.op === "remove" ? "-" : " "}</span>{" "}
                <span className="whitespace-pre-wrap break-words">{line.text}</span>
              </div>
            ))}
          </div>
        )}

        {interaction.question && <p className="mb-2 max-w-prose text-prose text-mist">{interaction.question}</p>}

        {interaction.answerable ? (
          <div className="flex flex-col gap-2">
            {interaction.options.map((option) => (
              <Button
                key={option.sendKey ?? option.label}
                variant="outline"
                className="h-auto min-h-11 w-full justify-start whitespace-normal px-3 py-2 text-left"
                onClick={() => setPendingOption(option)}
              >
                <span className="tabular mr-2 shrink-0 font-mono text-meta text-brass">{option.sendKey}</span>
                <span className="min-w-0 break-words">{option.label}</span>
              </Button>
            ))}
          </div>
        ) : (
          <div>
            <ul className="flex flex-col gap-1">
              {interaction.options.map((option) => (
                <li key={option.label} className="break-words text-body text-muted-ink">
                  • {option.label}
                </li>
              ))}
            </ul>
            {/* Honest about the limit, and specific about why, so this does not
                read as something that is merely unimplemented. */}
            <p className="mt-2 max-w-prose text-meta text-muted-ink">
              This prompt can't be answered from your phone: {interaction.parser || "this agent"} marks the selected
              choice with terminal styling that the relay's text read drops, so there's no way to tell what pressing
              Enter would pick. Open the console to answer it.
            </p>
            <Button asChild variant="outline" size="sm" className="mt-2">
              <Link to={`/console/${encodeURIComponent(run.paneId)}?generation=${run.generation}`}>
                <SquareTerminal className="size-4" /> Open console
              </Link>
            </Button>
          </div>
        )}
      </div>

      <ConfirmSendSheet
        run={run}
        option={pendingOption}
        onClose={() => setPendingOption(null)}
      />
    </section>
  );
}

/**
 * The confirmation step. It shows the literal key, quoted, before anything is
 * sent, and re-validates it against the same single-digit allowlist the relay used
 * to synthesize it — so a malformed key can never reach the mutation.
 */
function ConfirmSendSheet({
  run,
  option,
  onClose,
}: {
  run: Run;
  option: InterpretedOption | null;
  onClose: () => void;
}) {
  const { runPane, pending, error } = useMutations();
  const [sent, setSent] = useState(false);

  const key = option?.sendKey ?? null;
  const sendable = validSendKey(key);

  async function confirm() {
    if (!sendable) return;
    const res = await runPane("agent.send_keys", run, { keys: [key] });
    if (res && !("error" in res && res.error)) {
      setSent(true);
      onClose();
    }
  }

  return (
    <Sheet
      open={option !== null}
      onOpenChange={(next) => {
        if (!next) {
          setSent(false);
          onClose();
        }
      }}
    >
      <SheetContent aria-describedby="confirm-send-desc">
        <SheetHeader>
          <SheetTitle>Send this answer to {run.agentName}?</SheetTitle>
          <SheetDescription id="confirm-send-desc">
            This delivers one keystroke to the agent's input. Nothing else is sent, and the choice below is what this
            phone read off the screen — check it against the console if it matters.
          </SheetDescription>
        </SheetHeader>

        {option && (
          <div className="flex flex-col gap-3">
            <div className="rounded-log bg-hull p-3">
              <p className="text-meta text-faint-ink">Key delivered</p>
              <p className="tabular mt-0.5 font-mono text-prose text-mist">"{option.sendKey}"</p>
            </div>
            <div className="rounded-log bg-hull p-3">
              <p className="text-meta text-faint-ink">Which it selects</p>
              <p className="mt-0.5 break-words text-body text-mist">{option.label}</p>
            </div>
          </div>
        )}

        <div className="mt-4 flex gap-2">
          <Button variant="primary" className="flex-1" disabled={pending || !sendable} onClick={() => void confirm()}>
            {pending ? "Sending…" : "Send"}
          </Button>
          <Button variant="quiet" onClick={onClose} disabled={pending}>
            Cancel
          </Button>
        </div>

        {!sendable && option && (
          <p className="mt-2 text-meta text-flare" role="alert">
            This option has no valid key, so it can't be sent. Open the console instead.
          </p>
        )}
        {sent && (
          <p className="mt-2 text-meta text-tide" role="status" aria-live="polite">
            Sent.
          </p>
        )}
        {error && (
          <p className="mt-2 text-meta text-flare" role="alert">
            {error}
          </p>
        )}
      </SheetContent>
    </Sheet>
  );
}
