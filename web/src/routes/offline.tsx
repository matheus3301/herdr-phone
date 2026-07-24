import { OfflineState } from "@/components/states";
import { store } from "@/lib/store";

export function OfflineRoute() {
  return (
    <div className="flex h-full items-center justify-center">
      <OfflineState
        title="You're offline"
        description="Herdr Phone can't reach the relay. Reconnect to your tunnel, then retry."
        action={{ label: "Retry now", onClick: () => store.revalidate() }}
      />
    </div>
  );
}
