import type { ApiEnvelope, TokenResponse } from "@/lib/api/types";
import { backendOrigin, storeSession } from "@/lib/bff/session";

export async function POST(request: Request) {
  const response = await fetch(`${backendOrigin()}/api/v1/auth/register`, {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: await request.text(),
    cache: "no-store",
  });
  const payload = (await response.json().catch(() => null)) as ApiEnvelope<TokenResponse> | null;
  if (response.ok && payload && payload.code < 400) await storeSession(payload.data);
  return Response.json(payload ?? { code: response.status, message: "认证服务暂时不可用", data: null }, { status: response.status });
}
