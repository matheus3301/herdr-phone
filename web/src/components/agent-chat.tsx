import { Terminal, Wrench } from "lucide-react";
import { InterpretedNotice } from "@/components/interpreted-notice";
import type { InterpretedTranscript, InterpretedTurn } from "@/lib/interpreted";

/**
 * The interpreted transcript, rendered as a chat (SPEC §12.2).
 *
 * Two presentation rules keep this honest:
 *
 *  - **Agent prose is never styled like a quoted message.** It gets a plain
 *    reading column, not a speech bubble with an avatar, because a bubble asserts
 *    "the agent said this" and all we actually know is "these bytes appeared on
 *    screen and looked like prose".
 *  - **Tool calls and results stay visibly mechanical** — monospace, dimmed, and
 *    prefixed — so they never blend into prose.
 *
 * Text is rendered as text nodes (never innerHTML) and arrives already sanitized
 * and length-bounded by the relay.
 */
export function AgentChat({ transcript, unknownTurnKinds }: { transcript: InterpretedTranscript; unknownTurnKinds: string[] }) {
  return (
    <section aria-labelledby="chat-heading" className="flex flex-col gap-3">
      <div>
        <h2 id="chat-heading" className="text-body font-semibold text-mist">
          Conversation
        </h2>
        <InterpretedNotice parser={transcript.parser} className="mt-1 max-w-prose" />
      </div>

      {/* A bounded read of a busy pane very often opens part-way through an answer.
          Saying so matters: without it, a fragment starting mid-sentence reads as
          the agent's complete reply. */}
      {transcript.startsMidTurn && (
        <p className="text-meta text-muted-ink">
          This starts part-way through the agent's reply — the beginning is above what the relay could read. The console
          has the full scrollback.
        </p>
      )}

      {/* Announces additions only: the transcript is re-read on every refresh, and
          a polite region spanning replaced text would re-read the whole thing. */}
      <ol role="log" aria-live="polite" aria-relevant="additions" className="flex flex-col gap-2.5">
        {transcript.turns.map((turn) => (
          <li key={turn.id}>
            <Turn turn={turn} />
          </li>
        ))}
      </ol>

      {(transcript.droppedTurns > 0 || unknownTurnKinds.length > 0) && (
        <p className="text-meta text-muted-ink">
          {transcript.droppedTurns > 0 && (
            <>
              {transcript.droppedTurns === 1
                ? "1 earlier turn was dropped to fit the relay's limit."
                : `${transcript.droppedTurns} earlier turns were dropped to fit the relay's limit.`}{" "}
            </>
          )}
          {unknownTurnKinds.length > 0 && (
            <>
              {unknownTurnKinds.length === 1
                ? "1 turn this app does not understand was not shown."
                : `${unknownTurnKinds.length} turns this app does not understand were not shown.`}
            </>
          )}
        </p>
      )}
    </section>
  );
}

function Turn({ turn }: { turn: InterpretedTurn }) {
  switch (turn.kind) {
    case "agent_text":
      return <p className="max-w-prose whitespace-pre-wrap break-words text-prose text-mist">{turn.text}</p>;

    case "tool_call":
      return (
        <div className="flex items-start gap-2 rounded-log bg-hull px-3 py-2">
          <Wrench className="mt-0.5 size-3.5 shrink-0 text-faint-ink" aria-hidden="true" />
          <div className="min-w-0">
            {turn.tool && <span className="text-meta font-semibold text-mist">{turn.tool}</span>}
            {turn.text && (
              <pre className="mt-0.5 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[12.5px] leading-relaxed text-muted-ink">
                {turn.text}
              </pre>
            )}
          </div>
        </div>
      );

    case "tool_result":
      return (
        <div className="flex items-start gap-2 border-l-2 border-seam pl-3">
          <Terminal className="mt-0.5 size-3.5 shrink-0 text-faint-ink" aria-hidden="true" />
          <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[12.5px] leading-relaxed text-muted-ink">
            {turn.text}
          </pre>
        </div>
      );

    case "status":
      return <p className="tabular text-meta text-faint-ink">{turn.text}</p>;
  }
}
