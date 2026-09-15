export function normalizeServer(value: string): string {
  const u = new URL(value);
  if (u.username || u.password || u.search || u.hash || u.pathname !== "/")
    throw new Error("请输入服务的根地址，例如 https://browser.example.com");
  if (
    u.protocol !== "https:" &&
    !(u.protocol === "http:" && ["localhost", "127.0.0.1"].includes(u.hostname))
  )
    throw new Error("服务器需要使用 HTTPS");
  return u.origin;
}
export function handoffURL(server: string, target?: string) {
  const base = normalizeServer(server);
  if (!target) return base + "/";
  const u = new URL(target);
  if (!["https:", "http:"].includes(u.protocol))
    throw new Error("当前页面无法在云端打开，仅支持 HTTP/HTTPS 网址。");
  if (u.username || u.password) throw new Error("不支持包含登录凭据的网址。");
  return base + "/#" + new URLSearchParams({ open: u.href }).toString();
}
export async function openCloud(target?: string) {
  const { server } = await chrome.storage.local.get("server");
  if (!server) throw new Error("请先设置你的 Cloud Browser 服务地址。");
  await chrome.tabs.create({ url: handoffURL(server, target) });
}
