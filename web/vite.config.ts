import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { VitePWA } from "vite-plugin-pwa";
import { mockRelay } from "./mock/relay";

// Herdr Phone frontend build.
//
// Strict no-runtime-CDN: every asset (fonts via @fontsource, xterm CSS, icons)
// is bundled from node_modules or public/. The PWA service worker is authored
// by hand (injectManifest) so it can precache the shell yet refuse to cache any
// /api/ or terminal data (SPEC §14.4, §16).
//
// The mock relay plugin is node-side only (dev + preview). It never enters the
// browser bundle, so production builds have zero mock code. Playwright drives the
// preview server, which the mock relay backs with a deterministic in-memory herd.
export default defineConfig(({ mode }) => ({
  // Absolute asset base. Client routes are now nested (/runs/:id, /console/:pane),
  // so a relative base would resolve ./assets/* against the route's directory and
  // 404 on a deep link or a hard reload. The relay serves the shell from the
  // origin root, so "/" is correct in both production and preview.
  base: "/",
  define: {
    __APP_VERSION__: JSON.stringify("0.1.0"),
  },
  // Bind loopback IPv4 so Playwright's 127.0.0.1 probe (and the Go relay's
  // reverse proxy in production) reach the server deterministically.
  server: { host: "127.0.0.1", port: 5173, strictPort: false },
  preview: { host: "127.0.0.1", port: 4173, strictPort: true },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      strategies: "injectManifest",
      srcDir: "src",
      filename: "sw.ts",
      registerType: "prompt",
      injectRegister: null,
      manifest: false,
      injectManifest: {
        globPatterns: ["**/*.{js,css,html,woff,woff2,svg,png,ico,webmanifest}"],
        // Terminal + API responses must never be precached.
        globIgnores: ["**/api/**"],
      },
      devOptions: {
        enabled: false,
      },
    }),
    // Dev + preview mock backend; excluded from the client bundle.
    mockRelay(),
  ],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: mode !== "production",
    target: "es2022",
    rollupOptions: {
      output: {
        manualChunks: {
          xterm: ["@xterm/xterm", "@xterm/addon-fit"],
          react: ["react", "react-dom", "react-router-dom"],
        },
      },
    },
  },
}));
