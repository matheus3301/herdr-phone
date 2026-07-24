import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Self-hosted fonts (no runtime CDN). Spline Sans variable for UI; Commit Mono
// for terminal/IDs/utility labels (SPEC §14.2).
import "@fontsource-variable/spline-sans/index.css";
import "@fontsource/commit-mono/400.css";
import "@fontsource/commit-mono/500.css";
import "@fontsource/commit-mono/700.css";
import "@xterm/xterm/css/xterm.css";
import "./index.css";

import { App } from "./App";
import { setupPWA } from "./lib/pwa";

const el = document.getElementById("root");
if (!el) throw new Error("root element missing");

createRoot(el).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

setupPWA();
