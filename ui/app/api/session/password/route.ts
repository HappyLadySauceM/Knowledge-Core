import { proxyAuthenticatedRequest } from "@/lib/bff/proxy";

export async function PUT(request: Request) {
  return proxyAuthenticatedRequest(request, "/api/v1/users/me/password");
}
