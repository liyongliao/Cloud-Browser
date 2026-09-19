export {};

declare global {
  var __cloudBrowserBridgeInjected: boolean | undefined;
}

const channel = "cloud-browser-extension-v1";
const isTop = window === window.top;

function sendToCloud(type: string, detail: Record<string, unknown> = {}) {
  window.top?.postMessage({ channel, type, ...detail }, location.origin);
}

function sendRuntime(message: object) {
  return chrome.runtime.sendMessage(message).catch(() => ({ success: false }));
}

if (!globalThis.__cloudBrowserBridgeInjected) {
  globalThis.__cloudBrowserBridgeInjected = true;
  if (isTop) installTopBridge();
  if (location.pathname.startsWith("/view/")) installInputBridge();
}

function installTopBridge() {
  document.documentElement.dataset.cloudBrowserExtension = "0.3.1";
  sendToCloud("EXTENSION_READY", { version: "0.3.1" });
  window.addEventListener("message", (event) => {
    if (
      event.origin !== location.origin ||
      event.data?.channel !== channel
    )
      return;
    if (event.data.type === "EXTENSION_PING") {
      sendToCloud("EXTENSION_READY", { version: "0.3.1" });
    }
    if (event.data.type === "WRITE_LOCAL_CLIPBOARD") {
      void sendRuntime({
        type: "WRITE_CLIPBOARD",
        text: String(event.data.text ?? ""),
      });
    }
    if (event.data.type === "REQUEST_LOCAL_PASTE") {
      void sendRuntime({ type: "READ_CLIPBOARD" }).then((result) => {
        if (result?.success && typeof result.text === "string")
          sendToCloud("PASTE_FROM_LOCAL", { text: result.text });
      });
    }
    if (event.data.type === "OPEN_LOCAL_FILE_CHOOSER") {
      openLocalFileChooser(String(event.data.id), Boolean(event.data.multiple));
    }
  });
}

function installInputBridge() {
  const textarea = document.createElement("textarea");
  textarea.setAttribute("aria-hidden", "true");
  textarea.autocomplete = "off";
  textarea.autocapitalize = "off";
  textarea.spellcheck = false;
  Object.assign(textarea.style, {
    position: "fixed",
    left: "8px",
    bottom: "8px",
    width: "2px",
    height: "2px",
    opacity: "0.01",
    zIndex: "2147483647",
    resize: "none",
  });
  document.documentElement.appendChild(textarea);

  let composing = false;
  let pendingText = "";
  let flushTimer = 0;
  const flush = () => {
    flushTimer = 0;
    if (!pendingText) return;
    sendToCloud("INSERT_TEXT", { text: pendingText });
    pendingText = "";
  };
  const commit = () => {
    if (!textarea.value) return;
    pendingText += textarea.value;
    textarea.value = "";
    if (flushTimer) window.clearTimeout(flushTimer);
    flushTimer = window.setTimeout(flush, 24);
  };

  textarea.addEventListener("compositionstart", () => (composing = true));
  textarea.addEventListener("compositionend", () => {
    composing = false;
    window.setTimeout(commit, 0);
  });
  textarea.addEventListener("input", (event) => {
    if (!composing && !(event as InputEvent).isComposing) commit();
  });
  textarea.addEventListener("keydown", (event) => {
    if (composing || event.isComposing) return;
    const modifier = event.ctrlKey || event.metaKey;
    const key = event.key.toLowerCase();
    if (modifier && (key === "c" || key === "x")) {
      event.preventDefault();
      sendToCloud("COPY_TO_LOCAL", { action: key === "x" ? "cut" : "copy" });
      return;
    }
    if (modifier && key === "v") {
      event.preventDefault();
      void sendRuntime({ type: "READ_CLIPBOARD" }).then((result) => {
        if (result?.success && typeof result.text === "string")
          sendToCloud("PASTE_FROM_LOCAL", { text: result.text });
      });
      return;
    }
    const remoteKey = normalizeRemoteKey(event);
    if (remoteKey) {
      event.preventDefault();
      sendToCloud("REMOTE_KEY", { key: remoteKey });
    }
  });

  document.addEventListener(
    "pointerup",
    () => window.setTimeout(() => textarea.focus({ preventScroll: true }), 0),
    true,
  );
}

function normalizeRemoteKey(event: KeyboardEvent) {
  const names: Record<string, string> = {
    Enter: "Return",
    Escape: "Escape",
    Tab: "Tab",
    Backspace: "BackSpace",
    Delete: "Delete",
    ArrowLeft: "Left",
    ArrowRight: "Right",
    ArrowUp: "Up",
    ArrowDown: "Down",
    Home: "Home",
    End: "End",
    PageUp: "Page_Up",
    PageDown: "Page_Down",
  };
  if (names[event.key]) return names[event.key];
  if (/^F([1-9]|1[0-2])$/.test(event.key)) return event.key;
  if (event.altKey && event.key === "ArrowLeft") return "alt+Left";
  if (event.altKey && event.key === "ArrowRight") return "alt+Right";
  if (
    (event.ctrlKey || event.metaKey) &&
    /^[altrfwzy]$/i.test(event.key)
  )
    return `ctrl+${event.key.toLowerCase()}`;
  return "";
}

function openLocalFileChooser(id: string, multiple: boolean) {
  if (!isTop || !/^[a-f0-9]{32}$/.test(id)) return;
  const input = document.createElement("input");
  input.type = "file";
  input.multiple = multiple;
  input.hidden = true;
  document.documentElement.appendChild(input);

  const overlay = document.createElement("div");
  Object.assign(overlay.style, {
    position: "fixed",
    inset: "0",
    display: "grid",
    placeItems: "center",
    background: "rgba(9, 14, 24, .58)",
    zIndex: "2147483647",
  });
  const button = document.createElement("button");
  button.textContent = multiple ? "选择本地文件" : "选择一个本地文件";
  Object.assign(button.style, {
    border: "0",
    borderRadius: "12px",
    padding: "14px 22px",
    font: "600 15px system-ui",
    color: "white",
    background: "#4f75ff",
    boxShadow: "0 12px 36px rgba(0,0,0,.28)",
    cursor: "pointer",
  });
  overlay.appendChild(button);
  document.documentElement.appendChild(overlay);

  const close = () => {
    overlay.remove();
    input.remove();
  };
  button.addEventListener("click", () => input.click());
  overlay.addEventListener("click", (event) => {
    if (event.target === overlay) {
      sendToCloud("FILES_SELECTED", { id, files: [] });
      close();
    }
  });
  input.addEventListener("change", () => {
    sendToCloud("FILES_SELECTED", {
      id,
      files: Array.from(input.files ?? []),
    });
    close();
  });
  input.click();
}
