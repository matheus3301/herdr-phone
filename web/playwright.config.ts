import { defineConfig, devices } from "@playwright/test";

// Playwright drives the built app served by `vite preview`, which is backed by
// the mock relay (dev/preview only). This exercises the real production bundle
// against a deterministic in-memory herd — no Cloudflare, Herdr, or credentials.
const PORT = 4173;

export default defineConfig({
  testDir: "./e2e",
  // The mock relay is a single shared in-memory backend, so tests mutate common
  // state. Run serially (one worker) for deterministic, isolation-safe runs.
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // Required journeys must pass deterministically (SPEC §18/§22, review F3): no
  // retries, in CI or locally, so a newly-flaky test can never be masked green.
  retries: 0,
  workers: 1,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  timeout: 45_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "pixel-7",
      use: { ...devices["Pixel 7"] },
    },
    {
      name: "iphone-15",
      // Playwright ships "iPhone 15" descriptors; fall back to viewport if absent.
      use: {
        ...(devices["iPhone 15"] ?? devices["iPhone 14"]),
        browserName: "webkit",
      },
    },
    {
      name: "desktop",
      use: { browserName: "chromium", viewport: { width: 1440, height: 900 } },
    },
  ],
  webServer: {
    command: "npm run build && npm run preview",
    url: `http://127.0.0.1:${PORT}/`,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
});
