import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
const origin = process.env.ACCEPTANCE_ORIGIN;
const usersFile = process.env.ACCEPTANCE_USERS_FILE;
const concurrency = Number(process.env.ACCEPTANCE_CONCURRENCY ?? 3);
const duration = Number(process.env.ACCEPTANCE_SECONDS ?? 3600);
const targets = (process.env.ACCEPTANCE_URLS ?? "https://example.com").split(
  ",",
);
test.skip(
  !origin || !usersFile,
  "Set ACCEPTANCE_ORIGIN and ACCEPTANCE_USERS_FILE for a deployed x86_64 server.",
);
test(`${concurrency} independent cloud browsers stay connected, transfer files and revoke old controllers`, async ({
  browser,
}, testInfo) => {
  test.skip(
    !origin || !usersFile,
    "Set ACCEPTANCE_ORIGIN and ACCEPTANCE_USERS_FILE for a deployed x86_64 server.",
  );
  if (!Number.isFinite(duration) || duration < 1)
    throw new Error("Invalid ACCEPTANCE_SECONDS");
  test.setTimeout((duration + 360) * 1000);
  const users = JSON.parse(readFileSync(usersFile!, "utf8")) as Array<{
    email: string;
    password: string;
  }>;
  expect([1, 2, 3]).toContain(concurrency);
  expect(users).toHaveLength(concurrency);
  const contexts: BrowserContext[] = [];
  const pages: Page[] = [];
  const counters = Array.from({ length: concurrency }, () => 0);
  const csrf: string[] = [];
  const samples: unknown[] = [];
  async function login(index: number) {
    const c = await browser.newContext();
    contexts.push(c);
    const page = await c.newPage();
    await page.goto(origin!);
    await page.getByLabel("邮箱", { exact: true }).fill(users[index].email);
    await page.getByLabel("密码", { exact: true }).fill(users[index].password);
    await page.getByRole("button", { name: "登录并继续" }).click();
    await expect(
      page.getByRole("heading", { name: "你的浏览器，随处继续。" }),
    ).toBeVisible();
    return page;
  }
  async function operation(
    page: Page,
    index: number,
    kind: string,
    url?: string,
  ) {
    const r = await page.request.post(origin + "/api/v1/browser/" + kind, {
      headers: {
        Origin: origin!,
        "X-CSRF-Token": csrf[index],
        "Idempotency-Key": crypto.randomUUID(),
      },
      data: url ? { url } : {},
    });
    expect(r.status()).toBe(202);
    const { operationId } = await r.json();
    await expect
      .poll(
        async () => {
          const op = await page.request.get(
            origin + "/api/v1/operations/" + operationId,
          );
          return (await op.json()).state;
        },
        { timeout: 90000 },
      )
      .toBe("SUCCEEDED");
  }
  try {
    for (let i = 0; i < concurrency; i++) {
      const page = await login(i);
      pages.push(page);
      const me = await page.request.get(origin + "/api/v1/me");
      csrf[i] = (await me.json()).csrf;
      page.on("websocket", (socket) => {
        if (socket.url().includes("/websockify"))
          socket.on("framereceived", () => counters[i]++);
      });
      await page
        .getByRole("button", { name: /启动并继续浏览|接管并继续浏览/ })
        .click();
      await expect(page.getByTitle("远程 Chrome 浏览器")).toBeVisible({
        timeout: 90000,
      });
      await expect
        .poll(() => counters[i], { timeout: 30000 })
        .toBeGreaterThan(2);
      for (const url of targets) await operation(page, i, "open", url);
      const payload = Buffer.from(
        "Cloud Browser 文件往返验证 " + i + " " + crypto.randomUUID(),
      );
      const upload = await page.request.post(origin + "/api/v1/files", {
        headers: { Origin: origin!, "X-CSRF-Token": csrf[i] },
        multipart: {
          file: {
            name: "acceptance.txt",
            mimeType: "text/plain",
            buffer: payload,
          },
        },
      });
      expect(upload.status()).toBe(201);
      const file = await upload.json();
      const download = await page.request.get(
        origin + "/api/v1/files/" + file.id + "/download",
      );
      expect(download.status()).toBe(200);
      expect(await download.body()).toEqual(payload);
      const other = pages[(i + 1) % pages.length];
      if (other !== page) {
        const denied = await other.request.get(
          origin + "/api/v1/files/" + file.id + "/download",
        );
        expect(denied.status()).toBe(404);
      }
      const deleted = await page.request.delete(
        origin + "/api/v1/files/" + file.id,
        { headers: { Origin: origin!, "X-CSRF-Token": csrf[i] } },
      );
      expect(deleted.status()).toBe(200);
      let audioFrames = 0;
      page.on("websocket", (socket) => {
        if (socket.url().includes("/audio/"))
          socket.on("framereceived", () => audioFrames++);
      });
      await page.getByRole("button", { name: "声音", exact: true }).click();
      await expect
        .poll(() => audioFrames, { timeout: 20000 })
        .toBeGreaterThan(5);
    }
    const started = Date.now();
    while (Date.now() - started < duration * 1000) {
      for (const page of pages) {
        await expect(page.getByTitle("远程 Chrome 浏览器")).toBeVisible();
        const state = await page.request.get(origin + "/api/v1/browser");
        expect((await state.json()).state).toBe("RUNNING");
      }
      samples.push({
        elapsedSeconds: Math.round((Date.now() - started) / 1000),
        receivedFrames: [...counters],
      });
      await new Promise((r) =>
        setTimeout(
          r,
          Math.max(
            1,
            Math.min(15000, duration * 1000 - (Date.now() - started)),
          ),
        ),
      );
    }
    const replacement = await login(0);
    await replacement.getByRole("button", { name: "接管并继续浏览" }).click();
    await expect(replacement.getByTitle("远程 Chrome 浏览器")).toBeVisible({
      timeout: 30000,
    });
    await expect(pages[0].getByTitle("远程 Chrome 浏览器")).toHaveCount(0, {
      timeout: 15000,
    });
    // A duplicate website navigation can be side-effectful; the API dedupe test uses a benign URL.
    const key = crypto.randomUUID(),
      headers = {
        Origin: origin!,
        "X-CSRF-Token": csrf[0],
        "Idempotency-Key": key,
      };
    const first = await pages[0].request.post(origin + "/api/v1/browser/open", {
      headers,
      data: { url: targets[0] },
    });
    const second = await pages[0].request.post(
      origin + "/api/v1/browser/open",
      { headers, data: { url: targets[0] } },
    );
    expect((await first.json()).operationId).toBe(
      (await second.json()).operationId,
    );
    await testInfo.attach("connection-samples", {
      body: JSON.stringify(samples, null, 2),
      contentType: "application/json",
    });
  } finally {
    for (let i = 0; i < pages.length; i++) {
      try {
        await operation(pages[i], i, "stop");
      } catch {
        /* Report primary error; do not hide it with cleanup errors. */
      }
    }
    for (const context of contexts) await context.close();
  }
});
