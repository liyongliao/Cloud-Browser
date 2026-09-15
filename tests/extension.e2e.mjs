import { chromium } from "@playwright/test";
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
const profile = await mkdtemp(path.join(tmpdir(), "cb-extension-"));
const extension = path.resolve("extension/dist");
const context = await chromium.launchPersistentContext(profile, {
  channel: "chromium",
  headless: true,
  args: [
    `--disable-extensions-except=${extension}`,
    `--load-extension=${extension}`,
  ],
});
try {
  const worker =
    context.serviceWorkers()[0] ??
    (await context.waitForEvent("serviceworker", { timeout: 15000 }));
  const id = new URL(worker.url()).host;
  const page = await context.newPage();
  await page.goto(`chrome-extension://${id}/popup.html`);
  await page
    .getByLabel("Cloud Browser 地址")
    .fill("https://browser.example.test");
  await page.getByRole("button", { name: "保存地址" }).click();
  await page.getByText("服务器地址已保存").waitFor();
  const settings = await worker.evaluate(() =>
    chrome.storage.local.get("server"),
  );
  assert.equal(settings.server, "https://browser.example.test");
  await context.route("https://browser.example.test/**", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: "Cloud Browser handoff test destination",
    }),
  );
  const pending = context.waitForEvent("page");
  await page.getByRole("button", { name: "打开我的云浏览器 ↗" }).click();
  const opened = await pending;
  await opened.waitForURL("https://browser.example.test/");
  assert.equal(opened.url(), "https://browser.example.test/");
  console.log(
    "Extension installed; MV3 service worker, popup settings and cloud tab handoff passed.",
  );
} finally {
  await context.close();
  await rm(profile, { recursive: true, force: true });
}
