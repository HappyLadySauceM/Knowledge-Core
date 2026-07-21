import { accessToken, backendOrigin, clearSession, refreshToken } from "@/lib/bff/session";

export async function POST() {
  const refresh = await refreshToken();
  const access = await accessToken();
  if (refresh) {
    await fetch(`${backendOrigin()}/api/v1/auth/logout`, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json", ...(access ? { Authorization: `Bearer ${access}` } : {}) },
      body: JSON.stringify({ refresh_token: refresh }),
      cache: "no-store",
    }).catch(() => null);
  }
  await clearSession();
  return Response.json({ code: 0, message: "ok", data: null });
}
