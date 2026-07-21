import { cookies } from "next/headers";
import type { ApiEnvelope, TokenResponse } from "@/lib/api/types";

export const ACCESS_COOKIE = "kc_access";
export const REFRESH_COOKIE = "kc_refresh";
const apiOrigin = process.env.KNOWLEDGE_CORE_API_ORIGIN ?? "http://127.0.0.1:8080";

const cookieBase = {
  httpOnly: true,
  sameSite: "lax" as const,
  secure: process.env.NODE_ENV === "production",
  path: "/",
};

export async function storeSession(tokens: TokenResponse) {
  const cookieStore = await cookies();
  cookieStore.set(ACCESS_COOKIE, tokens.access_token, { ...cookieBase, maxAge: tokens.expires_in });
  cookieStore.set(REFRESH_COOKIE, tokens.refresh_token, { ...cookieBase, maxAge: 60 * 60 * 24 * 30 });
}

export async function clearSession() {
  const cookieStore = await cookies();
  cookieStore.set(ACCESS_COOKIE, "", { ...cookieBase, maxAge: 0 });
  cookieStore.set(REFRESH_COOKIE, "", { ...cookieBase, maxAge: 0 });
}

export async function accessToken() {
  return (await cookies()).get(ACCESS_COOKIE)?.value;
}

export async function refreshToken() {
  return (await cookies()).get(REFRESH_COOKIE)?.value;
}

export async function refreshSession() {
  const token = await refreshToken();
  if (!token) return null;
  const response = await fetch(`${apiOrigin}/api/v1/auth/refresh`, {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({ refresh_token: token }),
    cache: "no-store",
  });
  const payload = (await response.json().catch(() => null)) as ApiEnvelope<TokenResponse> | null;
  if (!response.ok || !payload || payload.code >= 400) {
    await clearSession();
    return null;
  }
  await storeSession(payload.data);
  return payload.data.access_token;
}

export function backendOrigin() {
  return apiOrigin;
}
