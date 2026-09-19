import { openCloud, serverPattern } from "./shared";

const bridgeScriptID = "cloud-browser-native-bridge";

async function configureBridge(server: string) {
  const pattern = serverPattern(server);
  const allowed = await chrome.permissions.contains({ origins: [pattern] });
  await chrome.scripting
    .unregisterContentScripts({ ids: [bridgeScriptID] })
    .catch(() => {});
  if (!allowed) return false;
  await chrome.scripting.registerContentScripts([
    {
      id: bridgeScriptID,
      matches: [pattern],
      js: ["content.js"],
      allFrames: true,
      matchOriginAsFallback: true,
      runAt: "document_start",
      persistAcrossSessions: true,
    },
  ]);
  const tabs = await chrome.tabs.query({ url: pattern });
  await Promise.all(
    tabs
      .filter((tab) => tab.id !== undefined)
      .map((tab) =>
        chrome.scripting
          .executeScript({
            target: { tabId: tab.id!, allFrames: true },
            files: ["content.js"],
          })
          .catch(() => undefined),
      ),
  );
  return true;
}

async function restoreBridge() {
  const { server } = await chrome.storage.local.get("server");
  if (server) await configureBridge(server);
}

async function ensureOffscreenDocument() {
  const url = chrome.runtime.getURL("offscreen.html");
  const contexts = await chrome.runtime.getContexts({
    contextTypes: [chrome.runtime.ContextType.OFFSCREEN_DOCUMENT],
    documentUrls: [url],
  });
  if (contexts.length) return;
  await chrome.offscreen.createDocument({
    url: "offscreen.html",
    reasons: [chrome.offscreen.Reason.CLIPBOARD],
    justification: "在用户按下复制或粘贴时同步本机与云端浏览器的文本剪贴板",
  });
}

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({
      id: "cloud-page",
      title: "在 Cloud Browser 中打开",
      contexts: ["page"],
      documentUrlPatterns: ["http://*/*", "https://*/*"],
    });
    chrome.contextMenus.create({
      id: "cloud-link",
      title: "在 Cloud Browser 中打开链接",
      contexts: ["link"],
      targetUrlPatterns: ["http://*/*", "https://*/*"],
    });
  });
  void restoreBridge();
});

chrome.runtime.onStartup.addListener(() => void restoreBridge());

chrome.contextMenus.onClicked.addListener((info, tab) => {
  const target =
    info.menuItemId === "cloud-link"
      ? info.linkUrl
      : (info.pageUrl ?? tab?.url);
  void openCloud(target).catch(async (error: Error) => {
    await chrome.storage.session.set({ lastError: error.message });
    await chrome.action.setBadgeText({ text: "!" });
    await chrome.action.setBadgeBackgroundColor({ color: "#b15b35" });
  });
});

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message.target === "offscreen") return;
  if (message.type === "CONFIGURE_SERVER") {
    configureBridge(message.server)
      .then((configured) => sendResponse({ success: configured }))
      .catch(() => sendResponse({ success: false }));
    return true;
  }
  if (message.type === "READ_CLIPBOARD" || message.type === "WRITE_CLIPBOARD") {
    ensureOffscreenDocument()
      .then(() => chrome.runtime.sendMessage({ ...message, target: "offscreen" }))
      .then(sendResponse)
      .catch(() =>
        sendResponse({ success: false, error: "CLIPBOARD_UNAVAILABLE" }),
      );
    return true;
  }
  if (message.type === "PING") {
    sendResponse({ pong: true, version: "0.3.1" });
  }
});
