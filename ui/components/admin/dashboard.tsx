"use client";

import Link from "next/link";
import { ArrowRight, FileText, FolderTree, Plus, Tags, Users } from "lucide-react";
import { useEffect, useState } from "react";
import { browserApi } from "@/lib/api/browser";
import type { Category, Document, ListResponse, Tag, User } from "@/lib/api/types";
import { formatRelativeDate } from "@/lib/format";

type DashboardData = { documents: ListResponse<Document>; categories: ListResponse<Category>; tags: ListResponse<Tag>; users: ListResponse<User> };

export function Dashboard() {
  const [data, setData] = useState<DashboardData | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    Promise.all([
      browserApi<ListResponse<Document>>("/api/backend/admin/documents?page=1&page_size=6"),
      browserApi<ListResponse<Category>>("/api/backend/admin/categories"),
      browserApi<ListResponse<Tag>>("/api/backend/admin/tags"),
      browserApi<ListResponse<User>>("/api/backend/admin/users?page=1&page_size=1"),
    ]).then(([documents, categories, tags, users]) => setData({ documents, categories, tags, users })).catch((reason) => setError(reason instanceof Error ? reason.message : "加载失败"));
  }, []);

  const stats = [
    { label: "文档", value: data?.documents.total ?? 0, icon: FileText, color: "text-[var(--brand)]" },
    { label: "分类", value: data?.categories.items.length ?? 0, icon: FolderTree, color: "text-[#087f6d]" },
    { label: "标签", value: data?.tags.items.length ?? 0, icon: Tags, color: "text-[#b35c1e]" },
    { label: "用户", value: data?.users.total ?? 0, icon: Users, color: "text-[#3272a8]" },
  ];
  return (
    <div className="mx-auto max-w-7xl px-5 py-8 lg:px-8">
      <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="text-sm text-[var(--muted)]">管理后台</p><h1 className="mt-1 text-2xl font-semibold">内容概览</h1></div><Link href="/admin/documents/new" className="inline-flex min-h-10 items-center gap-2 rounded-[var(--radius)] bg-[var(--brand)] px-3 text-sm font-medium text-white hover:bg-[var(--brand-hover)]"><Plus size={16} aria-hidden="true" /> 新建文档</Link></header>
      {error && <p role="alert" className="mt-6 rounded border border-[#efc7c4] bg-[#fff5f4] px-3 py-2 text-sm text-[var(--danger)]">{error}</p>}
      <section className="mt-8 grid gap-px overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--border)] sm:grid-cols-2 xl:grid-cols-4" aria-label="内容统计">{stats.map(({ label, value, icon: Icon, color }) => <div key={label} className="bg-[var(--surface)] p-5"><Icon size={19} className={color} aria-hidden="true" /><p className="mt-5 text-2xl font-semibold tabular-nums">{data ? value : "—"}</p><p className="mt-1 text-sm text-[var(--muted)]">{label}</p></div>)}</section>
      <section className="mt-8"><div className="mb-4 flex items-center justify-between"><h2 className="text-base font-semibold">最近文档</h2><Link href="/admin/documents" className="inline-flex items-center gap-1 text-sm text-[var(--muted)] hover:text-[var(--foreground)]">查看全部 <ArrowRight size={15} aria-hidden="true" /></Link></div><div className="overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)]"><div className="divide-y divide-[var(--border)]">{data?.documents.items.map((document) => <Link key={document.id} href={`/admin/documents/${document.id}/edit`} className="grid gap-2 px-4 py-3 hover:bg-[var(--surface-subtle)] sm:grid-cols-[1fr_100px_100px] sm:items-center"><div className="min-w-0"><p className="truncate text-sm font-medium">{document.title}</p><p className="mt-1 truncate text-xs text-[var(--faint)]">/{document.slug}</p></div><span className={`w-fit rounded px-2 py-1 text-xs ${document.status === "published" ? "bg-[#effaf4] text-[var(--success)]" : "bg-[var(--surface-muted)] text-[var(--muted)]"}`}>{document.status === "published" ? "已发布" : "草稿"}</span><span className="text-xs text-[var(--faint)]">{formatRelativeDate(document.updated_at)}</span></Link>)}{data && !data.documents.items.length && <p className="px-4 py-10 text-center text-sm text-[var(--muted)]">还没有文档。</p>}</div></div></section>
    </div>
  );
}
