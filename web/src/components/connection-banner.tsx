import { useEffect, useState } from "react";
import { useAppState } from "@/hooks/use-app-store";
import { store } from "@/lib/store";
import { CONNECTION_MESSAGES, reasonFor } from "@/lib/connection";
import { cn } from "@/lib/utils";

/**
 * One connection bar, driven by the store's single health clock so every
 * indicator agrees. It names *which* failure this is — the phone's network, the
 * relay link, or the Mac itself — because each has a different recovery.
 */
export function ConnectionBanner() {
  const { connection } = useAppState();
  const [online, setOnline] = useState(() => (typeof navigator === "undefined" ? true : navigator.onLine));

  useEffect(() => {
    const update = () => setOnline(navigator.onLine);
    window.addEventListener("online", update);
    window.addEventListener("offline", update);
    return () => {
      window.removeEventListener("online", update);
      window.removeEventListener("offline", update);
    };
  }, []);

  // navigator.onLine only *names* an already-detected failure; it never gates a
  // request and never raises a banner on its own.
  const reason = reasonFor(connection, online);
  if (!reason) return null;
  const message = CONNECTION_MESSAGES[reason];
  const danger = message.tone === "danger";

  return (
    <div
      role="status"
      aria-live="polite"
      className={cn(
        "flex items-center justify-between gap-3 px-4 py-2 text-meta",
        danger ? "bg-flare/15 text-flare" : "bg-brass/15 text-brass",
      )}
    >
      <span className="min-w-0">
        <span className="font-medium">{message.title}</span>
        <span className="ml-1.5 hidden text-muted-ink sm:inline">{message.detail}</span>
      </span>
      <button
        type="button"
        onClick={() => store.revalidate()}
        className={cn(
          "shrink-0 rounded-log px-2.5 py-1 ring-1 focus-visible:outline-2 focus-visible:outline-brass",
          danger ? "ring-flare/50 hover:bg-flare/15" : "ring-brass/50 hover:bg-brass/15",
        )}
      >
        Retry
      </button>
    </div>
  );
}
