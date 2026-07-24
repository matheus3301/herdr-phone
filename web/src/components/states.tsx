import type { ReactNode } from "react";
import { CircleAlert, Inbox, PlugZap } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

interface StateProps {
  title: string;
  /** Every state names the exact recovery action (SPEC §14.4). */
  description: string;
  icon?: ReactNode;
  action?: { label: string; onClick: () => void };
  tone?: "neutral" | "danger";
  className?: string;
}

function StateShell({ title, description, icon, action, tone = "neutral", className }: StateProps) {
  return (
    <div
      role={tone === "danger" ? "alert" : "status"}
      className={cn(
        "mx-auto flex max-w-sm flex-col items-center gap-3 px-6 py-10 text-center",
        className,
      )}
    >
      <div
        className={cn(
          "flex size-12 items-center justify-center rounded-[12px] border",
          tone === "danger" ? "border-flare/40 text-flare" : "border-frame text-muted-ink",
        )}
        aria-hidden
      >
        {icon}
      </div>
      <h2 className="text-base font-semibold text-mist">{title}</h2>
      <p className="text-sm text-muted-ink">{description}</p>
      {action && (
        <Button variant={tone === "danger" ? "danger" : "default"} onClick={action.onClick} className="mt-1">
          {action.label}
        </Button>
      )}
    </div>
  );
}

export function EmptyState(props: Omit<StateProps, "icon" | "tone">) {
  return <StateShell {...props} icon={<Inbox className="size-6" />} />;
}

export function ErrorState(props: Omit<StateProps, "icon" | "tone">) {
  return <StateShell {...props} tone="danger" icon={<CircleAlert className="size-6" />} />;
}

export function OfflineState(props: Omit<StateProps, "icon" | "tone">) {
  return <StateShell {...props} tone="danger" icon={<PlugZap className="size-6" />} />;
}
