import { normalizeServer, openCloud, serverPattern } from "./shared";
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
let { server } = await chrome.storage.local.get("server");
input.value = server ?? "";
if (!server) {
  document.querySelector<HTMLDetailsElement>("#settings")!.open = true;
} else if (
  !(await chrome.permissions.contains({ origins: [serverPattern(server)] }))
) {
  document.querySelector<HTMLDetailsElement>("#settings")!.open = true;
  message.textContent = "请重新保存服务器地址，以启用本机输入与文件桥接";
}
const { lastError } = await chrome.storage.session.get("lastError");
if (lastError) message.textContent = lastError;
await chrome.storage.session.remove("lastError");
await chrome.action.setBadgeText({ text: "" });
document.querySelector("#server-form")!.addEventListener("submit", (event) => {
  event.preventDefault();
  void action(async () => {
    const previousServer = server as string | undefined;
    const nextServer = normalizeServer(input.value.trim());
    const granted = await chrome.permissions.request({
      origins: [serverPattern(nextServer)],
    });
    if (!granted)
      throw new Error("需要允许访问该 Cloud Browser 地址才能启用输入与剪贴板桥接。");
    await chrome.storage.local.set({ server: nextServer });
    await chrome.runtime.sendMessage({ type: "CONFIGURE_SERVER", server: nextServer });
    if (previousServer && previousServer !== nextServer) {
      await chrome.permissions.remove({
        origins: [serverPattern(previousServer)],
      });
    }
    server = nextServer;
    input.value = nextServer;
    message.textContent = "服务器地址已保存，本机能力桥已启用";
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
