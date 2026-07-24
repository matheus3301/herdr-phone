import { createBrowserRouter, Navigate } from "react-router-dom";
import { RootLayout } from "@/components/root-layout";
import { TerminalRoute } from "@/routes/terminal";
import { HerdRoute } from "@/routes/herd";
import { SpacesRoute } from "@/routes/spaces";
import { SettingsRoute } from "@/routes/settings";
import { OfflineRoute } from "@/routes/offline";
import { RouteError } from "@/components/route-error";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <RootLayout />,
    errorElement: <RouteError />,
    children: [
      { index: true, element: <TerminalRoute /> },
      { path: "terminal", element: <TerminalRoute /> },
      { path: "herd", element: <HerdRoute /> },
      { path: "spaces", element: <SpacesRoute /> },
      { path: "settings", element: <SettingsRoute /> },
      { path: "offline", element: <OfflineRoute /> },
      { path: "*", element: <Navigate to="/" replace /> },
    ],
  },
]);
