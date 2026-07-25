import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  plugins: [react()],
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./vitest.setup.ts"],
    css: false,
    // The 5s default is enough now that the delivery-state tests commit their
    // value in one change event instead of typing per keystroke through jsdom,
    // which was the actual cause of their nondeterminism. A modest headroom over
    // the default covers Radix portal mounts on a loaded machine; it is not a
    // substitute for awaiting the thing under test.
    testTimeout: 10_000,
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    exclude: ["e2e/**", "node_modules/**"],
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      include: ["src/lib/**", "src/hooks/**", "src/components/**"],
    },
  },
});
