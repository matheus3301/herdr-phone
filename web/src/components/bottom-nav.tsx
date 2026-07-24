import { NavLink } from "react-router-dom";
import { SquareTerminal, LayoutGrid, Boxes } from "lucide-react";
import { useMemo } from "react";
import { useAppState } from "@/hooks/use-app-store";
import { needsYouCount } from "@/lib/triage";
import { cn } from "@/lib/utils";

const ITEMS = [
  { to: "/", label: "Terminal", icon: SquareTerminal, end: true },
  { to: "/herd", label: "Herd", icon: LayoutGrid, end: false },
  { to: "/spaces", label: "Spaces", icon: Boxes, end: false },
] as const;

export function BottomNav({ orientation = "horizontal" }: { orientation?: "horizontal" | "vertical" }) {
  const { snapshot } = useAppState();
  const needsYou = useMemo(() => (snapshot ? needsYouCount(snapshot.agents) : 0), [snapshot]);
  const vertical = orientation === "vertical";

  return (
    <nav
      aria-label="Primary"
      className={cn(
        "border-frame bg-bulkhead",
        vertical ? "flex flex-col gap-1 border-t p-2" : "flex border-t pb-[var(--spacing-safe-bottom)]",
      )}
    >
      {ITEMS.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.end}
          className={({ isActive }) =>
            cn(
              "relative flex items-center justify-center gap-2 focus-visible:outline-2 focus-visible:outline-brass",
              vertical ? "min-h-11 justify-start rounded-[10px] px-3" : "min-h-14 flex-1 flex-col py-1.5 text-[11px]",
              isActive ? "text-brass" : "text-muted-ink hover:text-mist",
            )
          }
        >
          <item.icon className={vertical ? "size-5" : "size-6"} />
          <span className={cn("font-utility uppercase tracking-wide", vertical ? "text-sm" : "text-[10px]")}>
            {item.label}
          </span>
          {item.label === "Herd" && needsYou > 0 && (
            <span
              className="absolute right-[22%] top-1.5 size-2 rounded-full bg-flare"
              aria-hidden
            />
          )}
        </NavLink>
      ))}
    </nav>
  );
}
