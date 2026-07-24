import { cva } from "class-variance-authority";

export const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-[10px] text-sm font-medium transition-colors select-none disabled:pointer-events-none disabled:opacity-45 focus-visible:outline-2 focus-visible:outline-brass focus-visible:outline-offset-2 [&_svg]:size-[18px] [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default:
          "bg-bulkhead text-mist border border-frame seam hover:bg-[color-mix(in_srgb,var(--color-bulkhead)_80%,var(--color-mist)_8%)] active:translate-y-px",
        primary:
          "bg-brass text-onaccent border border-[color-mix(in_srgb,var(--color-brass)_70%,black)] font-semibold hover:brightness-105 active:translate-y-px",
        ok: "bg-tide text-onaccent border border-[color-mix(in_srgb,var(--color-tide)_70%,black)] font-semibold hover:brightness-105 active:translate-y-px",
        danger:
          "bg-flare text-onaccent border border-[color-mix(in_srgb,var(--color-flare)_70%,black)] font-semibold hover:brightness-105 active:translate-y-px",
        ghost: "text-mist hover:bg-bulkhead/70 border border-transparent",
        outline: "border border-frame text-mist bg-transparent hover:bg-bulkhead/60",
      },
      size: {
        default: "h-11 px-4 py-2",
        sm: "h-9 px-3 text-[13px]",
        lg: "h-12 px-6 text-base",
        icon: "h-11 w-11",
        key: "h-11 min-w-11 px-2 font-utility text-[13px]",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);
