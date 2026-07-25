import { createBrowserRouter, Navigate } from "react-router-dom";
import { RootLayout } from "@/components/root-layout";
import { AgentsPlaceholder } from "@/routes/agents-placeholder";
import { RunRoute } from "@/routes/run";
import { StartRunRoute } from "@/routes/run-new";
import { WorkspacesRoute } from "@/routes/workspaces";
import { WorkspaceDetailRoute } from "@/routes/workspace-detail";
import { ConsoleRoute } from "@/routes/console";
import { SettingsRoute } from "@/routes/settings";
import { OfflineRoute } from "@/routes/offline";
import { RouteError } from "@/components/route-error";

/**
 * Run-centric routes. `/` is the agent inbox — the shell renders it as the left
 * column and every other route fills the detail column, so navigation never
 * unmounts the inbox.
 */
export const router = createBrowserRouter([
  {
    path: "/",
    element: <RootLayout />,
    errorElement: <RouteError />,
    children: [
      { index: true, element: <AgentsPlaceholder /> },
      { path: "runs/new", element: <StartRunRoute /> },
      { path: "runs/:runId", element: <RunRoute /> },
      { path: "workspaces", element: <WorkspacesRoute /> },
      { path: "workspaces/:workspaceId", element: <WorkspaceDetailRoute /> },
      { path: "console/:paneId", element: <ConsoleRoute /> },
      { path: "settings", element: <SettingsRoute /> },
      { path: "offline", element: <OfflineRoute /> },
      { path: "*", element: <Navigate to="/" replace /> },
    ],
  },
]);
