import { fallbackCategories, fallbackDocumentList, fallbackDocuments, fallbackTags } from "./fallback";
import { getPublicCategories, getPublicDocumentBySlug, getPublicDocuments, getPublicTags } from "./client";

export async function listPublicDocuments(query: { page?: number; pageSize?: number; q?: string; category?: string; tag?: string } = {}) {
  try {
    const result = await getPublicDocuments(query);
    return process.env.NODE_ENV === "development" && result.items.length === 0 ? fallbackDocumentList(query) : result;
  } catch {
    return fallbackDocumentList(query);
  }
}

export async function findPublicDocument(slug: string) {
  try {
    return await getPublicDocumentBySlug(slug);
  } catch {
    return fallbackDocuments.find((document) => document.slug === slug) ?? null;
  }
}

export async function listPublicCategories() {
  try {
    const items = (await getPublicCategories()).items;
    return process.env.NODE_ENV === "development" && items.length === 0 ? fallbackCategories : items;
  } catch {
    return fallbackCategories;
  }
}

export async function listPublicTags() {
  try {
    const items = (await getPublicTags()).items;
    return process.env.NODE_ENV === "development" && items.length === 0 ? fallbackTags : items;
  } catch {
    return fallbackTags;
  }
}
