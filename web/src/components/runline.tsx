import { useEffect, useRef, useState } from "react";
import { relativeTime } from "@/lib/format";
import type { ObservedEvent } from "@/lib/run-store";

/**
 * The runline — the product's one distinctive element.
 *
 * A restrained vertical flight recorder: a hairline rail with a tick per entry,
 * rendered as a semantic ordered list so a screen reader gets "list, 4 items"
 * rather than a wall of divs. The only motion in the product is a new entry
 * settling into place, and it is disabled under reduced motion.
 *
 * Every entry describes something this device observed. Herdr publishes no
 * wall-clock transition time, so each is stamped with when the phone saw it and
 * says so. No terminal byte is ever promoted into a runline entry.
 */
export function Runline({ events, now }: { events: ObservedEvent[]; now: number }) {
  const [fresh, setFresh] = useState<Set<string>>(() => new Set());
  const known = useRef<Set<string> | null>(null);

  useEffect(() => {
    if (known.current === null) {
      // Entries present on the first paint are history, not new activity —
      // opening a run must not replay its whole feed as an animation.
      known.current = new Set(events.map((e) => e.id));
      return;
    }
    const added = events.filter((e) => !known.current!.has(e.id)).map((e) => e.id);
    if (added.length === 0) return;
    for (const id of added) known.current!.add(id);
    setFresh(new Set(added));
  }, [events]);

  if (events.length === 0) return null;

  return (
    <ol className="runline">
      {events.map((event) => (
        <li key={event.id} data-tone={event.tone} data-fresh={fresh.has(event.id) ? "true" : undefined} className="py-1.5">
          <p className="text-body text-mist">{event.text}</p>
          <p className="tabular text-faint-ink">seen {relativeTime(event.at, now)}</p>
        </li>
      ))}
    </ol>
  );
}
