import * as React from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Bottom sheet. Used for advanced controls and pickers that would otherwise
 * crowd a phone screen. Respects the safe-area bottom inset so it never sits
 * under the home indicator.
 */
export const Sheet = DialogPrimitive.Root;
export const SheetTrigger = DialogPrimitive.Trigger;
export const SheetClose = DialogPrimitive.Close;

function SheetOverlay({ className, ...props }: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>) {
  return <DialogPrimitive.Overlay className={cn("fixed inset-0 z-50 bg-deck/75", className)} {...props} />;
}

export const SheetContent = React.forwardRef<
  React.ComponentRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content> & { hideClose?: boolean }
>(({ className, children, hideClose = false, ...props }, ref) => (
  <DialogPrimitive.Portal>
    <SheetOverlay />
    <DialogPrimitive.Content
      ref={ref}
      className={cn(
        "fixed inset-x-0 bottom-0 z-50 flex max-h-[85dvh] flex-col gap-3 overflow-y-auto",
        "rounded-t-[14px] border-t border-frame bg-bulkhead",
        "px-4 pt-3 pb-[calc(16px+var(--spacing-safe-bottom))]",
        "shadow-[0_-10px_30px_rgba(0,0,0,0.35)] focus:outline-none",
        className,
      )}
      {...props}
    >
      <div className="mx-auto mb-1 h-1 w-9 shrink-0 rounded-full bg-frame" aria-hidden />
      {children}
      {!hideClose && (
        <DialogPrimitive.Close
          className="absolute right-2 top-2 flex size-11 items-center justify-center rounded-log text-muted-ink hover:text-mist focus-visible:outline-2 focus-visible:outline-brass"
          aria-label="Close"
        >
          <X className="size-5" />
        </DialogPrimitive.Close>
      )}
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
));
SheetContent.displayName = "SheetContent";

export function SheetHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex flex-col gap-1 pr-10", className)} {...props} />;
}
export function SheetTitle({ className, ...props }: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title className={cn("text-prose font-semibold text-mist", className)} {...props} />;
}
export function SheetDescription({
  className,
  ...props
}: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description className={cn("text-body text-muted-ink", className)} {...props} />;
}
