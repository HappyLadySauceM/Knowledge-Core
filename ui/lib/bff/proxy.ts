import { NextResponse } from "next/server";
import { accessToken, backendOrigin, refreshSession } from "./session";

const responseHeaders = ["content-type", "content-disposition", "cache-control"];

async function backendFetch(request: Request, path: string, token?: string) {
  const headers = new Headers();
  const contentType = request.headers.get("content-type");
  if (contentType) headers.set("Content-Type", contentType);
  headers.set("Accept", "application/json");
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const method = request.method.toUpperCase();
  const body = method === "GET" || method === "HEAD" ? undefined : await request.arrayBuffer();
  return fetch(`${backendOrigin()}${path}`, { method, headers, body, cache: "no-store" });
}

function toNextResponse(response: Response) {
  const headers = new Headers();
  for (const name of responseHeaders) {
    const value = response.headers.get(name);
    if (value) headers.set(name, value);
  }
  return new NextResponse(response.body, { status: response.status, headers });
}

export async function proxyAuthenticatedRequest(request: Request, path: string) {
  let response = await backendFetch(request.clone(), path, await accessToken());
  if (response.status === 401) {
    const token = await refreshSession();
    if (token) response = await backendFetch(request.clone(), path, token);
  }
  return toNextResponse(response);
}
