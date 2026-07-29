import { FlaskConical } from "lucide-react";

/**
 * The standing label on every interpreted surface.
 *
 * It is not dismissible, and that is the point. Everything below it was produced
 * by pattern-matching a third-party TUI's screen output, not by any agent
 * publishing its own messages — so a reader who scrolls into the middle of the
 * chat has to be able to tell. Removing this, or letting it be hidden, would turn
 * a labelled guess into an apparent transcript.
 */
export function InterpretedNotice({ parser, className }: { parser: string; className?: string }) {
  return (
    <p className={className}>
      <span className="inline-flex items-center gap-1.5 rounded-log bg-brass/12 px-2 py-1 text-meta text-mist ring-1 ring-brass/40">
        <FlaskConical className="size-3.5" aria-hidden="true" />
        Experimental reading
      </span>{" "}
      <span className="text-meta text-muted-ink">
        Assembled by matching {parser ? <span className="text-mist">{parser}</span> : "this agent"}'s on-screen output.
        These are not the agent's own messages, they can be wrong, and they break when the agent's interface changes.
        The console is the only faithful view.
      </span>
    </p>
  );
}
