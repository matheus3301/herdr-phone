import { cn } from "@/lib/utils";

/**
 * Placeholder block. Static by default — a pulsing shimmer would be the second
 * animation in a product that allows exactly one, and status must never be
 * carried by motion.
 */
export function Skeleton({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div aria-hidden className={cn("rounded-log bg-bulkhead", className)} {...props} />;
}
