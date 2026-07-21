import type { ApiEnvelope } from "./types";

export class BrowserApiError extends Error {
  constructor(message: string, public readonly status: number) {
    super(message);
    this.name = "BrowserApiError";
  }
}

export async function browserApi<T>(path: string, init?: RequestInit) {
  const response = await fetch(path, {
    ...init,
    headers: { Accept: "application/json", ...(init?.body instanceof FormData ? {} : { "Content-Type": "application/json" }), ...init?.headers },
    cache: "no-store",
  });
  const payload = await response.json().catch(() => null) as ApiEnvelope<T> | null;
  if (!response.ok || !payload || payload.code >= 400) throw new BrowserApiError(payload?.message ?? "请求失败", response.status);
  return payload.data;
}
