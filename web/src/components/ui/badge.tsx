import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

/**
 * A quiet label. Sentence case, never uppercase mono — the old tiny-caps chips
 * made every screen read like a diagnostic panel.
 */
const badgeVariants = cva("inline-flex items-center gap-1.5 rounded-[5px] px-1.5 py-0.5 text-meta font-medium", {
  variants: {
    tone: {
      neutral: "bg-bulkhead text-muted-ink ring-1 ring-seam",
      brass: "bg-brass/15 text-brass",
      tide: "bg-tide/15 text-tide",
      flare: "bg-flare/15 text-flare",
      mist: "bg-mist/10 text-mist",
    },
  },
  defaultVariants: { tone: "neutral" },
});

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement>, VariantProps<typeof badgeVariants> {}

export function Badge({ className, tone, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ tone, className }))} {...props} />;
}
