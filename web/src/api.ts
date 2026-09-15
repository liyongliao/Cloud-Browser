import type { components } from "./schema";
export type User = components["schemas"]["User"];
export type Browser = components["schemas"]["Browser"];
export type Operation = components["schemas"]["Operation"];
export type FileEntry = components["schemas"]["File"];
export type Lease = components["schemas"]["Lease"];
let csrf = "";
export function setCSRF(value: string) {
  csrf = value;
}
export class APIError extends Error {
  constructor(
    public code: string,
    public status: number,
  ) {
    super(messages[code] ?? code);
  }
}
const messages: Record<string, string> = {
  UNAUTHENTICATED: "登录已过期，请重新登录。",
  INVALID_CREDENTIALS: "邮箱或密码不正确。",
  CAPACITY_FULL: "当前浏览器名额已满，请稍后再试。",
  MEMORY_LOW: "服务器内存不足，请稍后再试。",
  DISK_LOW: "服务器存储空间不足，请联系管理员。",
  QUOTA_EXCEEDED: "浏览器存储已达到限额，请清理文件。",
  CONTROL_REVOKED: "控制权已在其他设备接管。",
  BROWSER_START_TIMEOUT: "浏览器启动超时，请稍后重试。",
  OPEN_RESULT_UNKNOWN:
    "无法确认链接是否已打开，请先查看云端标签页，再决定是否重试。",
  PASSWORD_LENGTH: "密码需要 12–256 字节。",
  CURRENT_PASSWORD_INVALID: "当前密码不正确。",
  PASSWORD_UNCHANGED: "新密码不能与当前密码相同。",
  INVALID_INVITE: "邀请已过期或已被使用。",
  RATE_LIMITED: "操作过于频繁，请稍后再试。",
  RUNNER_UNAVAILABLE: "浏览器服务暂时无法连接。",
  BROWSER_NOT_RUNNING: "浏览器尚未运行。",
  UPLOAD_TOO_LARGE: "文件超过 200 MB 或剩余存储空间不足。",
};
export async function request<T>(
  path: string,
  method = "GET",
  body?: unknown,
  headers: Record<string, string> = {},
): Promise<T> {
  const multipart = body instanceof FormData;
  let response: Response;
  try {
    response = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: {
        ...(body && !multipart ? { "Content-Type": "application/json" } : {}),
        ...(method !== "GET" ? { "X-CSRF-Token": csrf } : {}),
        ...headers,
      },
      body: body ? (multipart ? body : JSON.stringify(body)) : undefined,
    });
  } catch {
    throw new Error("网络连接中断，请检查网络后重试。");
  }
  const data = await response
    .json()
    .catch(() => ({ error: "INVALID_RESPONSE" }));
  if (!response.ok) throw new APIError(data.error, response.status);
  return data as T;
}
export async function operate(
  kind: "start" | "open" | "stop",
  url?: string,
): Promise<Operation> {
  const { operationId } = await request<{ operationId: string }>(
    "/api/v1/browser/" + kind,
    "POST",
    url ? { url } : {},
    { "Idempotency-Key": crypto.randomUUID() },
  );
  for (let i = 0; i < 150; i++) {
    await new Promise((resolve) => setTimeout(resolve, 1000));
    const op = await request<Operation>("/api/v1/operations/" + operationId);
    if (op.state === "SUCCEEDED") return op;
    if (op.state === "FAILED" || op.state === "UNKNOWN")
      throw new APIError(op.error, 409);
  }
  throw new Error("操作仍在处理中，可以稍后刷新查看浏览器状态。");
}
