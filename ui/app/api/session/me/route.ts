import { proxyAuthenticatedRequest } from "@/lib/bff/proxy";

export async function GET(request: Request) {
  return proxyAuthenticatedRequest(request, "/api/v1/users/me");
}

export async function PATCH(request: Request) {
  return proxyAuthenticatedRequest(request, "/api/v1/users/me");
}
