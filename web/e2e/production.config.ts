import { defineConfig } from "@playwright/test";

const baseURL = process.env.MEGADW_BASE_URL ?? "http://127.0.0.1:18080";

export default defineConfig({
  testDir: ".",
  testMatch: /production\.spec\.ts/,
  fullyParallel: false,
  retries: 0,
  reporter: "list",
  use: {
    baseURL,
    trace: "retain-on-failure",
    viewport: { width: 1280, height: 900 },
  },
});
