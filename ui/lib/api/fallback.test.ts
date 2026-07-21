import { describe, expect, it } from "vitest";
import { fallbackDocumentList } from "./fallback";

describe("fallbackDocumentList", () => {
  it("orders public content by publication time descending", () => {
    const result = fallbackDocumentList();
    const timestamps = result.items.map((document) => new Date(document.published_at ?? 0).getTime());
    expect(timestamps).toEqual([...timestamps].sort((left, right) => right - left));
  });

  it("filters by keyword, category, and tag", () => {
    expect(fallbackDocumentList({ q: "React" }).items.map((document) => document.slug)).toEqual(["react-hooks-guide"]);
    expect(fallbackDocumentList({ category: "reading" }).items.every((document) => document.category?.slug === "reading")).toBe(true);
    expect(fallbackDocumentList({ tag: "ai" }).items.every((document) => document.tags.some((tag) => tag.slug === "ai"))).toBe(true);
  });
});
