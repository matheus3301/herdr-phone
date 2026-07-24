import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Self-hosted fonts (no runtime CDN). Public Sans variable carries navigation,
// conversation, and body copy; IBM Plex Mono is restricted to commands, paths,
// ids, timestamps, and the console.
import "@fontsource-variable/public-sans/index.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
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
