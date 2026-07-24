import { cn } from "@/lib/utils";

/** The field-instrument brand mark (matches the app icon), inline SVG. */
export function BrandMark({ className, beacon = false }: { className?: string; beacon?: boolean }) {
  return (
    <svg viewBox="0 0 512 512" className={cn("size-6", className)} role="img" aria-label="Herdr Phone">
      <rect x="96" y="96" width="320" height="320" rx="28" className="fill-bulkhead" />
      <rect x="96" y="96" width="320" height="320" rx="28" fill="none" className="stroke-frame" strokeWidth="6" />
      <path
        d="M150 232 l40 24 l-40 24"
        fill="none"
        className="stroke-mist"
        strokeWidth="18"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <rect x="212" y="272" width="88" height="16" rx="8" className="fill-tide" />
      <circle cx="344" cy="256" r="26" className={cn("fill-brass", beacon && "animate-beacon")} />
    </svg>
  );
}
