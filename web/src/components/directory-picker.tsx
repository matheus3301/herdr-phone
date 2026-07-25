import { useEffect, useState } from "react";
import { ChevronRight, CornerLeftUp, Folder } from "lucide-react";
import * as api from "@/lib/api";
import { Button } from "@/components/ui/button";
import { shortPath } from "@/lib/format";
import type { DirectoryListing } from "@/lib/types";

/**
 * Directory-only browser confined to the relay's allowed roots (SPEC §12
 * /directories, §15). No file reads, no uploads — directory selection only.
 */
export function DirectoryPicker({ value, onChange }: { value: string; onChange: (path: string) => void }) {
  const [listing, setListing] = useState<DirectoryListing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .listDirectories(value)
      .then((l) => {
        if (!cancelled) setListing(l);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof api.ApiError ? err.message : "Cannot list directory");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [value]);

  return (
    <div className="rounded-[10px] border border-seam bg-hull">
      <div className="flex items-center justify-between gap-2 border-b border-seam px-3 py-2">
        <span className="truncate tabular text-tide" title={value}>
          {shortPath(value, 3)}
        </span>
        {listing?.parent && (
          <Button
            variant="ghost"
            size="sm"
            className="h-8"
            onClick={() => onChange(listing.parent as string)}
            aria-label="Up one directory"
          >
            <CornerLeftUp className="size-4" /> Up
          </Button>
        )}
      </div>
      <div className="max-h-44 overflow-y-auto">
        {loading && <p className="px-3 py-3 text-sm text-muted-ink">Loading…</p>}
        {error && (
          <p className="px-3 py-3 text-sm text-flare" role="alert">
            {error}
          </p>
        )}
        {!loading &&
          !error &&
          listing?.entries.map((e) => (
            <button
              key={e.path}
              type="button"
              onClick={() => onChange(e.path)}
              className="flex min-h-10 w-full items-center gap-2 px-3 py-2 text-left text-sm text-mist hover:bg-bulkhead focus-visible:outline-2 focus-visible:outline-brass"
            >
              <Folder className="size-4 text-muted-ink" />
              <span className="flex-1 truncate">{e.name}</span>
              <ChevronRight className="size-4 text-muted-ink" />
            </button>
          ))}
        {!loading && !error && listing?.entries.length === 0 && (
          <p className="px-3 py-3 text-sm text-muted-ink">No subdirectories.</p>
        )}
      </div>
    </div>
  );
}
