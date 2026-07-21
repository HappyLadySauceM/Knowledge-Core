import { proxyAuthenticatedRequest } from "@/lib/bff/proxy";

type RouteContext = { params: Promise<{ path: string[] }> };
const allowedRoots = new Set(["admin", "users", "assets"]);

async function handler(request: Request, context: RouteContext) {
  const { path } = await context.params;
  if (!path.length || !allowedRoots.has(path[0])) return Response.json({ code: 404, message: "Not Found", data: null }, { status: 404 });
  const target = new URL(`/api/v1/${path.map(encodeURIComponent).join("/")}`, "http://local");
  target.search = new URL(request.url).search;
  return proxyAuthenticatedRequest(request, `${target.pathname}${target.search}`);
}

export const GET = handler;
export const POST = handler;
export const PUT = handler;
export const PATCH = handler;
export const DELETE = handler;
