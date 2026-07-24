import { cva } from "class-variance-authority";

/**
 * Dispatch Log button. Quiet by default — a hairline and a surface shift, not an
 * outlined chip — so a screen full of actions does not read as a control panel.
 * `primary` is reserved for the one deliberate action on a screen. Every size
 * clears the 44px touch target except `chip`, which is only ever used inside a
 * row that is itself at least 44px tall.
 */
export const buttonVariants = cva(
  [
    "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-log",
    "text-body font-medium transition-colors select-none",
    "disabled:pointer-events-none disabled:opacity-45",
    "focus-visible:outline-2 focus-visible:outline-brass focus-visible:outline-offset-2",
    "[&_svg]:size-[18px] [&_svg]:shrink-0",
  ].join(" "),
  {
    variants: {
      variant: {
        default: "bg-bulkhead text-mist ring-1 ring-seam hover:bg-[color-mix(in_srgb,var(--color-bulkhead)_88%,var(--color-mist))]",
        primary: "bg-brass text-onaccent font-semibold hover:brightness-110 active:brightness-95",
        ok: "bg-tide text-onaccent font-semibold hover:brightness-110 active:brightness-95",
        danger: "bg-flare text-onaccent font-semibold hover:brightness-110 active:brightness-95",
        ghost: "text-mist hover:bg-bulkhead",
        quiet: "text-muted-ink hover:bg-bulkhead hover:text-mist",
        outline: "ring-1 ring-frame text-mist bg-transparent hover:bg-bulkhead",
      },
      size: {
        default: "h-11 px-4",
        sm: "h-11 px-3 text-meta",
        lg: "h-12 px-6",
        icon: "h-11 w-11",
        chip: "h-9 px-2.5 text-meta",
        key: "h-11 min-w-11 px-2 font-mono text-meta",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);
