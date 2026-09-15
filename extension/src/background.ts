import { openCloud } from "./shared";
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
});
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
