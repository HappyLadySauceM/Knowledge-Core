import "server-only";

import type { ApiEnvelope, Category, Document, ListResponse, Tag } from "./types";

const apiOrigin = process.env.KNOWLEDGE_CORE_API_ORIGIN ?? "http://127.0.0.1:8080";

export class ApiClientError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
  ) {
    super(message);
    this.name = "ApiClientError";
  }
}

async function request<T>(path: string, init?: RequestInit & { next?: NextFetchRequestConfig }) {
  const response = await fetch(`${apiOrigin}${path}`, {
    ...init,
    headers: { Accept: "application/json", ...init?.headers },
  });
  const payload = (await response.json().catch(() => null)) as ApiEnvelope<T> | null;
  if (!response.ok || !payload || payload.code >= 400) {
    throw new ApiClientError(payload?.message ?? "请求失败", response.status, payload?.message);
  }
  return payload.data;
}

export function getPublicDocuments(query: { page?: number; pageSize?: number; q?: string; category?: string; tag?: string } = {}) {
  const params = new URLSearchParams();
  params.set("page", String(query.page ?? 1));
  params.set("page_size", String(query.pageSize ?? 12));
  if (query.q) params.set("q", query.q);
  if (query.category) params.set("category", query.category);
  if (query.tag) params.set("tag", query.tag);
  return request<ListResponse<Document>>(`/api/v1/documents?${params.toString()}`, {
    next: { revalidate: 60, tags: ["public-documents"] },
  });
}

export function getPublicDocumentBySlug(slug: string) {
  return request<Document>(`/api/v1/documents/slug/${encodeURIComponent(slug)}`, {
    next: { revalidate: 60, tags: [`public-document:${slug}`] },
  });
}

export function getPublicCategories() {
  return request<ListResponse<Category>>("/api/v1/categories", {
    next: { revalidate: 300, tags: ["public-categories"] },
  });
}

export function getPublicTags() {
  return request<ListResponse<Tag>>("/api/v1/tags", {
    next: { revalidate: 300, tags: ["public-tags"] },
  });
}
