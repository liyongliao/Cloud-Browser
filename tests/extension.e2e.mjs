import { chromium } from "@playwright/test";
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
const profile = await mkdtemp(path.join(tmpdir(), "cb-extension-"));
const extension = path.resolve("extension/dist");
const deadline = (label, promise) =>
  Promise.race([
    promise,
    new Promise((_, reject) =>
      setTimeout(() => reject(new Error(`${label} timed out`)), 5000),
    ),
  ]);
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
  await worker.evaluate(() =>
    chrome.storage.local.set({ server: "https://browser.example.test" }),
  );
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

  await opened.evaluate(() => {
    Object.assign(window.chrome, {
      runtime: { sendMessage: async () => ({ success: true, text: "" }) },
    });
  });
  await opened.addScriptTag({ path: path.resolve("extension/dist/content.js") });
  assert.equal(
    await opened.locator("html").getAttribute("data-cloud-browser-extension"),
    "0.3.0",
  );
  const bridge = await deadline("bridge handshake", opened.evaluate(
    () =>
      new Promise((resolve) => {
        const channel = "cloud-browser-extension-v1";
        const receive = (event) => {
          if (event.data?.channel === channel && event.data.type === "EXTENSION_READY") {
            window.removeEventListener("message", receive);
            resolve(event.data.version);
          }
        };
        window.addEventListener("message", receive);
        window.postMessage({ channel, type: "EXTENSION_PING" }, location.origin);
      }),
  ));
  assert.equal(bridge, "0.3.0");

  const viewer = await context.newPage();
  await viewer.goto("https://browser.example.test/view/test/vnc.html");
  await viewer.evaluate(() => {
    Object.assign(window.chrome, {
      runtime: { sendMessage: async () => ({ success: true, text: "" }) },
    });
  });
  await viewer.addScriptTag({ path: path.resolve("extension/dist/content.js") });
  const inserted = viewer.evaluate(
    () =>
      new Promise((resolve) => {
        window.addEventListener("message", (event) => {
          if (event.data?.type === "INSERT_TEXT") resolve(event.data.text);
        });
      }),
  );
  await viewer.locator("body").click();
  await viewer.waitForFunction(() => document.activeElement?.tagName === "TEXTAREA");
  await viewer.keyboard.insertText("中文输入");
  assert.equal(await deadline("IME bridge", inserted), "中文输入");

  await opened.evaluate(() =>
    window.postMessage(
      {
        channel: "cloud-browser-extension-v1",
        type: "OPEN_LOCAL_FILE_CHOOSER",
        id: "0123456789abcdef0123456789abcdef",
        multiple: false,
      },
      location.origin,
    ),
  );
  await opened.getByRole("button", { name: "选择一个本地文件" }).waitFor();
  console.log(
    "Extension installed; scoped bridge, IME input, file chooser and cloud handoff passed.",
  );
} finally {
  await context.close();
  await rm(profile, { recursive: true, force: true });
}
