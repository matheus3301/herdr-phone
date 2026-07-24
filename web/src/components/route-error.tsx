import { useRouteError } from "react-router-dom";
import { ErrorState } from "./states";

export function RouteError() {
  const error = useRouteError();
  const message =
    error instanceof Error ? error.message : typeof error === "string" ? error : "This view failed to render.";
  return (
    <div className="flex h-dvh items-center justify-center bg-deck">
      <ErrorState
        title="Something went wrong"
        description={message}
        action={{ label: "Reload", onClick: () => window.location.reload() }}
      />
    </div>
  );
}
