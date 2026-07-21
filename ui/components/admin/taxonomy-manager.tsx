"use client";

import { FormEvent, useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TextField } from "@/components/ui/text-field";
import { browserApi } from "@/lib/api/browser";
import type { Category, ListResponse, Tag } from "@/lib/api/types";

export function TaxonomyManager() {
  const [categories, setCategories] = useState<Category[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [categoryName, setCategoryName] = useState("");
  const [categorySlug, setCategorySlug] = useState("");
  const [tagName, setTagName] = useState("");
  const [tagSlug, setTagSlug] = useState("");
  const [error, setError] = useState("");

  async function load() {
    try {
      const [categoryData, tagData] = await Promise.all([browserApi<ListResponse<Category>>("/api/backend/admin/categories"), browserApi<ListResponse<Tag>>("/api/backend/admin/tags")]);
      setCategories(categoryData.items);
      setTags(tagData.items);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "加载失败"); }
  }
  useEffect(() => {
    let cancelled = false;
    Promise.all([browserApi<ListResponse<Category>>("/api/backend/admin/categories"), browserApi<ListResponse<Tag>>("/api/backend/admin/tags")])
      .then(([categoryData, tagData]) => {
        if (cancelled) return;
        setCategories(categoryData.items);
        setTags(tagData.items);
      })
      .catch((reason) => { if (!cancelled) setError(reason instanceof Error ? reason.message : "加载失败"); });
    return () => { cancelled = true; };
  }, []);

  async function createCategory(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!categoryName.trim()) return;
    try { await browserApi("/api/backend/admin/categories", { method: "POST", body: JSON.stringify({ name: categoryName.trim(), slug: categorySlug.trim() }) }); setCategoryName(""); setCategorySlug(""); await load(); } catch (reason) { setError(reason instanceof Error ? reason.message : "创建分类失败"); }
  }
  async function createTag(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!tagName.trim()) return;
    try { await browserApi("/api/backend/admin/tags", { method: "POST", body: JSON.stringify({ name: tagName.trim(), slug: tagSlug.trim() }) }); setTagName(""); setTagSlug(""); await load(); } catch (reason) { setError(reason instanceof Error ? reason.message : "创建标签失败"); }
  }
  async function remove(path: string, name: string) {
    if (!window.confirm(`确定删除“${name}”吗？`)) return;
    try { await browserApi(path, { method: "DELETE" }); await load(); } catch (reason) { setError(reason instanceof Error ? reason.message : "删除失败"); }
  }

  return (
    <div className="mx-auto max-w-5xl px-5 py-8 lg:px-8"><header><p className="text-sm text-[var(--muted)]">内容组织</p><h1 className="mt-1 text-2xl font-semibold">分类与标签</h1></header>{error && <p role="alert" className="mt-5 rounded border border-[#efc7c4] bg-[#fff5f4] px-3 py-2 text-sm text-[var(--danger)]">{error}</p>}<div className="mt-8 grid gap-8 lg:grid-cols-2"><section><div className="mb-4 flex items-center justify-between"><h2 className="text-base font-semibold">分类</h2><span className="text-xs text-[var(--faint)]">{categories.length} 个</span></div><form onSubmit={createCategory} className="mb-4 grid grid-cols-1 gap-2 sm:grid-cols-[1fr_1fr_auto]"><TextField id="new-category" aria-label="新分类名称" placeholder="名称" value={categoryName} onChange={(event) => setCategoryName(event.target.value)} required /><TextField id="new-category-slug" aria-label="新分类 Slug" placeholder="slug" pattern="[a-z0-9-]+" value={categorySlug} onChange={(event) => setCategorySlug(event.target.value)} required /><Button type="submit" variant="brand" aria-label="创建分类"><Plus size={16} aria-hidden="true" /></Button></form><div className="divide-y divide-[var(--border)] rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)]">{categories.map((category) => <div key={category.id} className="flex items-center justify-between px-3 py-3"><div><p className="text-sm font-medium">{category.name}</p><p className="mt-1 text-xs text-[var(--faint)]">{category.document_count} 篇文档 · {category.slug}</p></div><Button type="button" variant="ghost" className="size-8 min-h-8 px-0 text-[var(--danger)]" onClick={() => remove(`/api/backend/admin/categories/${category.id}`, category.name)}><Trash2 size={15} aria-hidden="true" /><span className="sr-only">删除 {category.name}</span></Button></div>)}{!categories.length && <p className="px-3 py-8 text-center text-sm text-[var(--muted)]">还没有分类。</p>}</div></section><section><div className="mb-4 flex items-center justify-between"><h2 className="text-base font-semibold">标签</h2><span className="text-xs text-[var(--faint)]">{tags.length} 个</span></div><form onSubmit={createTag} className="mb-4 grid grid-cols-1 gap-2 sm:grid-cols-[1fr_1fr_auto]"><TextField id="new-tag" aria-label="新标签名称" placeholder="名称" value={tagName} onChange={(event) => setTagName(event.target.value)} required /><TextField id="new-tag-slug" aria-label="新标签 Slug" placeholder="slug" pattern="[a-z0-9-]+" value={tagSlug} onChange={(event) => setTagSlug(event.target.value)} required /><Button type="submit" variant="brand" aria-label="创建标签"><Plus size={16} aria-hidden="true" /></Button></form><div className="divide-y divide-[var(--border)] rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)]">{tags.map((tag) => <div key={tag.id} className="flex items-center justify-between px-3 py-3"><div><p className="text-sm font-medium">{tag.name}</p><p className="mt-1 text-xs text-[var(--faint)]">{tag.document_count} 篇文档 · {tag.slug}</p></div><Button type="button" variant="ghost" className="size-8 min-h-8 px-0 text-[var(--danger)]" onClick={() => remove(`/api/backend/admin/tags/${tag.id}`, tag.name)}><Trash2 size={15} aria-hidden="true" /><span className="sr-only">删除 {tag.name}</span></Button></div>)}{!tags.length && <p className="px-3 py-8 text-center text-sm text-[var(--muted)]">还没有标签。</p>}</div></section></div></div>
  );
}
