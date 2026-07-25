import * as React from "react";
import * as CollapsiblePrimitive from "@radix-ui/react-collapsible";
import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";

export const Collapsible = CollapsiblePrimitive.Root;
export const CollapsibleContent = CollapsiblePrimitive.Content;

/**
 * Disclosure trigger with a rotating caret. Radix wires `aria-expanded` and
 * `aria-controls`, which is what makes the runline a navigable list of
 * collapsible parts rather than a pile of divs.
 */
export const CollapsibleTrigger = React.forwardRef<
  React.ComponentRef<typeof CollapsiblePrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof CollapsiblePrimitive.Trigger> & { hideCaret?: boolean }
>(({ className, children, hideCaret = false, ...props }, ref) => (
  <CollapsiblePrimitive.Trigger
    ref={ref}
    className={cn(
      "group flex min-h-11 w-full items-center gap-2 rounded-log text-left text-body text-mist",
      "focus-visible:outline-2 focus-visible:outline-brass focus-visible:outline-offset-2",
      className,
    )}
    {...props}
  >
    {!hideCaret && (
      <ChevronRight
        className="size-4 shrink-0 text-muted-ink transition-transform group-data-[state=open]:rotate-90"
        aria-hidden
      />
    )}
    {children}
  </CollapsiblePrimitive.Trigger>
));
CollapsibleTrigger.displayName = "CollapsibleTrigger";
