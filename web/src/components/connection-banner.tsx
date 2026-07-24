import { useAppState } from "@/hooks/use-app-store";
import { store } from "@/lib/store";
import { cn } from "@/lib/utils";

/**
 * One connection bar, driven by the store's single health clock (SPEC §16) so
 * every indicator agrees. Amber "reconnecting" after trouble, red "lost" with a
 * Retry after the lost threshold. Hidden when live.
 */
export function ConnectionBanner() {
  const { connection } = useAppState();
  if (connection === "live" || connection === "connecting") return null;

  const lost = connection === "lost";
  return (
    <div
      role="status"
      aria-live="polite"
      className={cn(
        "flex items-center justify-between gap-3 px-4 py-1.5 text-[13px] font-utility",
        lost ? "bg-flare/20 text-flare border-b border-flare/40" : "bg-brass/15 text-brass border-b border-brass/30",
      )}
    >
      <span className="flex items-center gap-2">
        <span className={cn("size-2 rounded-full", lost ? "bg-flare" : "bg-brass animate-beacon")} aria-hidden />
        {lost ? "Connection lost" : "Reconnecting…"}
      </span>
      {lost && (
        <button
          type="button"
          onClick={() => store.revalidate()}
          className="rounded-md border border-flare/50 px-2 py-0.5 text-flare hover:bg-flare/15 focus-visible:outline-2 focus-visible:outline-brass"
        >
          Retry
        </button>
      )}
    </div>
  );
}
