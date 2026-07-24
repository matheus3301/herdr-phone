import { NavLink } from "react-router-dom";
import { Boxes, Inbox, Plus } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Primary navigation: Agents, Start run, Workspaces.
 *
 * The console is deliberately absent. It is reachable in one tap from any run
 * and from every pane's menu, but it is a recovery surface, not a destination
 * the product organises itself around.
 *
 * The same element serves both layouts — a bottom bar on a phone, a left rail at
 * desktop width — so crossing the breakpoint restyles rather than remounts.
 */
const ITEMS = [
  { to: "/", label: "Agents", icon: Inbox, end: true },
  { to: "/runs/new", label: "Start run", icon: Plus, end: false },
  { to: "/workspaces", label: "Workspaces", icon: Boxes, end: false },
] as const;

export function PrimaryNav({ attention }: { attention: number }) {
  return (
    <nav
      aria-label="Primary"
      className={cn(
        "shell-nav flex border-t border-seam bg-bulkhead pb-[var(--spacing-safe-bottom)]",
        "lg:flex-col lg:items-center lg:gap-1 lg:border-r lg:border-t-0 lg:px-2 lg:py-3 lg:pb-3",
      )}
    >
      {ITEMS.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.end}
          className={({ isActive }) =>
            cn(
              "relative flex min-w-0 flex-1 flex-col items-center justify-center gap-1 py-2 text-[11px]",
              "min-h-14 focus-visible:outline-2 focus-visible:outline-brass focus-visible:outline-offset-[-2px]",
              "lg:size-14 lg:flex-none lg:rounded-log",
              isActive ? "text-brass lg:bg-brass/12" : "text-muted-ink hover:text-mist",
            )
          }
        >
          <item.icon className="size-[22px] shrink-0" aria-hidden />
          <span className="max-w-full truncate px-1">{item.label}</span>
          {item.label === "Agents" && attention > 0 && (
            <>
              <span
                className="absolute right-[calc(50%-1.4rem)] top-1.5 size-2 rounded-full bg-flare lg:right-2.5 lg:top-2"
                aria-hidden
              />
              {/* The dot is decoration. Assistive technology gets the count, which
                  is the actual information — the inbox announces an *arrival*
                  once, but the persistent indicator needs a textual equivalent. */}
              <span className="sr-only">
                {attention} {attention === 1 ? "run needs" : "runs need"} you
              </span>
            </>
          )}
        </NavLink>
      ))}
    </nav>
  );
}
