"use client";

import Link from "next/link";
import { FilePlus2, Pencil, Search, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { browserApi } from "@/lib/api/browser";
import type { Document, ListResponse } from "@/lib/api/types";
import { formatDate } from "@/lib/format";

export function DocumentList() {
  const [documents, setDocuments] = useState<Document[]>([]);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    const params = new URLSearchParams({ page: "1", page_size: "50" });
    if (query.trim()) params.set("q", query.trim());
    if (status) params.set("status", status);
    try {
      const data = await browserApi<ListResponse<Document>>(`/api/backend/admin/documents?${params}`);
      setDocuments(data.items);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "加载失败");
    } finally {
      setLoading(false);
    }
  }, [query, status]);

  useEffect(() => {
    let cancelled = false;
    const params = new URLSearchParams({ page: "1", page_size: "50" });
    if (query.trim()) params.set("q", query.trim());
    if (status) params.set("status", status);
    browserApi<ListResponse<Document>>(`/api/backend/admin/documents?${params}`)
      .then((data) => { if (!cancelled) setDocuments(data.items); })
      .catch((reason) => { if (!cancelled) setError(reason instanceof Error ? reason.message : "加载失败"); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [query, status]);

  async function remove(document: Document) {
    if (!window.confirm(`确定删除“${document.title}”吗？此操作无法撤销。`)) return;
    try {
      await browserApi(`/api/backend/admin/documents/${document.id}`, { method: "DELETE" });
      await load();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "删除失败");
    }
  }

  return (
    <div className="mx-auto max-w-7xl px-5 py-8 lg:px-8">
      <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="text-sm text-[var(--muted)]">内容管理</p><h1 className="mt-1 text-2xl font-semibold">文档</h1></div><Link href="/admin/documents/new" className="inline-flex min-h-10 items-center gap-2 rounded-[var(--radius)] bg-[var(--brand)] px-3 text-sm font-medium text-white hover:bg-[var(--brand-hover)]"><FilePlus2 size={16} aria-hidden="true" /> 新建文档</Link></header>
      <div className="mt-7 flex flex-col gap-3 border-y border-[var(--border)] py-4 sm:flex-row"><form className="flex flex-1 items-center gap-2 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] px-3" onSubmit={(event) => { event.preventDefault(); void load(); }}><Search size={16} className="text-[var(--faint)]" aria-hidden="true" /><label htmlFor="document-search" className="sr-only">搜索文档</label><input id="document-search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索标题或摘要" className="min-h-10 flex-1 bg-transparent text-sm outline-none" /></form><label className="sr-only" htmlFor="document-status">发布状态</label><select id="document-status" value={status} onChange={(event) => setStatus(event.target.value)} className="min-h-10 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] px-3 text-sm"><option value="">全部状态</option><option value="draft">草稿</option><option value="published">已发布</option></select></div>
      {error && <p role="alert" className="mt-5 rounded border border-[#efc7c4] bg-[#fff5f4] px-3 py-2 text-sm text-[var(--danger)]">{error}</p>}
      <div className="mt-5 overflow-x-auto rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)]"><table className="w-full min-w-[760px] border-collapse text-left text-sm"><thead className="bg-[var(--surface-subtle)] text-xs text-[var(--muted)]"><tr><th className="px-4 py-3 font-medium">标题</th><th className="px-4 py-3 font-medium">分类</th><th className="px-4 py-3 font-medium">状态</th><th className="px-4 py-3 font-medium">发布时间</th><th className="px-4 py-3 text-right font-medium">操作</th></tr></thead><tbody className="divide-y divide-[var(--border)]">{documents.map((document) => <tr key={document.id} className="hover:bg-[var(--surface-subtle)]"><td className="max-w-sm px-4 py-3"><p className="truncate font-medium">{document.title}</p><p className="mt-1 truncate text-xs text-[var(--faint)]">/{document.slug}</p></td><td className="px-4 py-3 text-[var(--muted)]">{document.category?.name ?? "—"}</td><td className="px-4 py-3"><span className={`rounded px-2 py-1 text-xs ${document.status === "published" ? "bg-[#effaf4] text-[var(--success)]" : "bg-[var(--surface-muted)] text-[var(--muted)]"}`}>{document.status === "published" ? "已发布" : "草稿"}</span></td><td className="px-4 py-3 text-[var(--muted)]">{formatDate(document.published_at)}</td><td className="px-4 py-3"><div className="flex justify-end gap-1"><Link href={`/admin/documents/${document.id}/edit`} className="grid size-9 place-items-center rounded text-[var(--muted)] hover:bg-[var(--surface-muted)] hover:text-[var(--foreground)]" title="编辑文档"><Pencil size={16} aria-hidden="true" /><span className="sr-only">编辑 {document.title}</span></Link><Button type="button" variant="ghost" className="size-9 min-h-9 px-0 text-[var(--danger)]" title="删除文档" onClick={() => remove(document)}><Trash2 size={16} aria-hidden="true" /><span className="sr-only">删除 {document.title}</span></Button></div></td></tr>)}</tbody></table>{loading && <p className="px-4 py-10 text-center text-sm text-[var(--muted)]">正在加载...</p>}{!loading && !documents.length && <p className="px-4 py-10 text-center text-sm text-[var(--muted)]">没有符合条件的文档。</p>}</div>
    </div>
  );
}
