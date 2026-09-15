import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "tests/acceptance",
  workers: 1,
  retries: 0,
  reporter: [
    ["list"],
    ["html", { outputFolder: "artifacts/acceptance-report", open: "never" }],
  ],
  use: {
    browserName: "chromium",
    headless: true,
    trace: "off",
    screenshot: "off",
  },
  outputDir: "artifacts/acceptance-results",
});
