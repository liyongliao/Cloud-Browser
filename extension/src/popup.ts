import { normalizeServer, openCloud } from "./shared";
const message = document.querySelector<HTMLParagraphElement>("#message")!;
const input = document.querySelector<HTMLInputElement>("#server")!;
async function action(fn: () => Promise<void>) {
  message.textContent = "";
  try {
    await fn();
  } catch (e) {
    message.textContent = e instanceof Error ? e.message : "操作失败";
  }
}
const { server } = await chrome.storage.local.get("server");
input.value = server ?? "";
if (!server)
  document.querySelector<HTMLDetailsElement>("#settings")!.open = true;
const { lastError } = await chrome.storage.session.get("lastError");
if (lastError) message.textContent = lastError;
await chrome.storage.session.remove("lastError");
await chrome.action.setBadgeText({ text: "" });
document.querySelector("#server-form")!.addEventListener("submit", (event) => {
  event.preventDefault();
  void action(async () => {
    const server = normalizeServer(input.value.trim());
    await chrome.storage.local.set({ server });
    input.value = server;
    message.textContent = "服务器地址已保存";
  });
});
document
  .querySelector("#continue")!
  .addEventListener("click", () => void action(() => openCloud()));
document.querySelector("#open")!.addEventListener(
  "click",
  () =>
    void action(async () => {
      const [tab] = await chrome.tabs.query({
        active: true,
        currentWindow: true,
      });
      if (!tab?.url) throw new Error("无法读取当前页面的网址。");
      await openCloud(tab.url);
    }),
);
