import { BrandMark } from "./brand-mark";

/** First-paint splash shown while booting/pairing. */
export function BootSplash({ label }: { label: string }) {
  return (
    <div
      className="flex h-dvh w-full flex-col items-center justify-center gap-4 bg-deck text-mist"
      style={{ paddingTop: "var(--spacing-safe-top)", paddingBottom: "var(--spacing-safe-bottom)" }}
    >
      <BrandMark className="size-16" beacon />
      <div className="tabular uppercase tracking-widest text-muted-ink" role="status" aria-live="polite">
        {label}
      </div>
    </div>
  );
}
