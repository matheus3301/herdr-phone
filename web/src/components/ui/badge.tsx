import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-[6px] border px-2 py-0.5 font-utility text-[11px] uppercase tracking-wide",
  {
    variants: {
      tone: {
        neutral: "border-frame bg-bulkhead text-muted-ink",
        brass: "border-brass/50 bg-brass/15 text-brass",
        tide: "border-tide/50 bg-tide/15 text-tide",
        flare: "border-flare/50 bg-flare/15 text-flare",
        mist: "border-mist/30 bg-mist/10 text-mist",
      },
    },
    defaultVariants: { tone: "neutral" },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, tone, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ tone, className }))} {...props} />;
}
