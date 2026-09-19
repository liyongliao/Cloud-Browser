chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message.target !== "offscreen") return;
  if (message.type === "READ_CLIPBOARD") {
    navigator.clipboard
      .readText()
      .then((text) => sendResponse({ success: true, text }))
      .catch(() => sendResponse({ success: false, error: "CLIPBOARD_READ_FAILED" }));
    return true;
  }
  if (message.type === "WRITE_CLIPBOARD") {
    navigator.clipboard
      .writeText(String(message.text ?? ""))
      .then(() => sendResponse({ success: true }))
      .catch(() => sendResponse({ success: false, error: "CLIPBOARD_WRITE_FAILED" }));
    return true;
  }
});
