import * as React from "react";
import { cn } from "@/lib/utils";

export interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {
  /** Grow with content up to this many CSS pixels, then scroll. */
  maxHeight?: number;
}

/**
 * Auto-sizing textarea.
 *
 * Height is recomputed from `scrollHeight` on every value change so the field
 * grows with the instruction instead of hiding it behind a one-line window. The
 * growth is capped so the composer can never eat the run above it.
 *
 * IME composition is deliberately NOT handled here — the parent owns Enter
 * semantics and needs the composition state to decide whether Enter commits a
 * candidate or sends the instruction.
 */
export const Textarea = React.forwardRef<HTMLTextAreaElement, TextareaProps>(
  ({ className, maxHeight = 160, onChange, value, rows = 1, ...props }, forwarded) => {
    const inner = React.useRef<HTMLTextAreaElement | null>(null);

    const setRef = React.useCallback(
      (node: HTMLTextAreaElement | null) => {
        inner.current = node;
        if (typeof forwarded === "function") forwarded(node);
        else if (forwarded) (forwarded as React.MutableRefObject<HTMLTextAreaElement | null>).current = node;
      },
      [forwarded],
    );

    const resize = React.useCallback(() => {
      const node = inner.current;
      if (!node) return;
      node.style.height = "auto";
      const next = Math.min(node.scrollHeight, maxHeight);
      node.style.height = `${next}px`;
      node.style.overflowY = node.scrollHeight > maxHeight ? "auto" : "hidden";
    }, [maxHeight]);

    React.useLayoutEffect(resize, [resize, value]);

    return (
      <textarea
        ref={setRef}
        rows={rows}
        value={value}
        onChange={(e) => {
          onChange?.(e);
          resize();
        }}
        className={cn(
          "block w-full resize-none rounded-log bg-hull px-3 py-2.5 text-prose text-mist ring-1 ring-seam",
          "placeholder:text-faint-ink focus-visible:outline-2 focus-visible:outline-brass focus-visible:outline-offset-1",
          "disabled:cursor-not-allowed disabled:opacity-60",
          className,
        )}
        {...props}
      />
    );
  },
);
Textarea.displayName = "Textarea";
