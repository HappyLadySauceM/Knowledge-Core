"use client";

import "@blocknote/core/fonts/inter.css";
import "@blocknote/mantine/style.css";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { BlockNoteView } from "@blocknote/mantine";
import { useCreateBlockNote } from "@blocknote/react";
import { ArrowLeft, Check, CloudUpload, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TextArea, TextField } from "@/components/ui/text-field";
import { browserApi } from "@/lib/api/browser";
import type { Asset, Category, Document, ListResponse, Tag } from "@/lib/api/types";

type EditorProps = { documentId?: string };

export function DocumentEditor({ documentId }: EditorProps) {
  const router = useRouter();
  const [document, setDocument] = useState<Document | null>(null);
  const [categories, setCategories] = useState<Category[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [title, setTitle] = useState("");
  const [slug, setSlug] = useState("");
  const [summary, setSummary] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [selectedTags, setSelectedTags] = useState<number[]>([]);
  const [status, setStatus] = useState<"draft" | "published">("draft");
  const [coverUrl, setCoverUrl] = useState("");
  const [source, setSource] = useState("manual");
  const [confidence, setConfidence] = useState("1");
  const [ready, setReady] = useState(!documentId);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");
  const markdownLoaded = useRef(false);

  const uploadFile = useCallback(async (file: File) => {
    const formData = new FormData();
    formData.append("file", file);
    const asset = await browserApi<Asset>("/api/backend/admin/assets", { method: "POST", body: formData });
    return asset.url;
  }, []);

  const editor = useCreateBlockNote({ uploadFile });

  useEffect(() => {
    Promise.all([
      browserApi<ListResponse<Category>>("/api/backend/admin/categories"),
      browserApi<ListResponse<Tag>>("/api/backend/admin/tags"),
      documentId ? browserApi<Document>(`/api/backend/admin/documents/${documentId}`) : Promise.resolve(null),
    ]).then(([categoryData, tagData, loaded]) => {
      setCategories(categoryData.items);
      setTags(tagData.items);
      if (loaded) {
        setDocument(loaded);
        setTitle(loaded.title);
        setSlug(loaded.slug);
        setSummary(loaded.summary);
        setCategoryId(String(loaded.category_id || ""));
        setSelectedTags(loaded.tags.map((tag) => tag.id));
        setStatus(loaded.status);
        setCoverUrl(loaded.cover_url);
        setSource(loaded.source || "manual");
        setConfidence(String(loaded.confidence || 1));
      }
      setReady(true);
    }).catch((reason) => setError(reason instanceof Error ? reason.message : "加载编辑器失败"));
  }, [documentId]);

  useEffect(() => {
    if (!document?.content || markdownLoaded.current) return;
    markdownLoaded.current = true;
    Promise.resolve(editor.tryParseMarkdownToBlocks(document.content)).then((blocks) => {
      editor.replaceBlocks(editor.document, blocks);
    }).catch(() => setError("正文加载失败，请重新输入。"));
  }, [document, editor]);

  const heading = useMemo(() => documentId ? "编辑文档" : "新建文档", [documentId]);

  async function save(nextStatus = status) {
    setSaving(true);
    setSaved(false);
    setError("");
    try {
      const content = await editor.blocksToMarkdownLossy(editor.document);
      const blocks = await Promise.all(editor.document.map(async (block, index) => ({
        block_id: block.id || crypto.randomUUID(),
        parent_id: "",
        position_key: String(index + 1).padStart(8, "0"),
        type: block.type,
        content_json: JSON.stringify(block),
        text_content: (await editor.blocksToMarkdownLossy([block])).trim(),
      })));
      const payload = { slug: slug.trim(), title: title.trim(), summary: summary.trim(), content, category_id: Number(categoryId) || 0, tag_ids: selectedTags, source, status: nextStatus, confidence: Number(confidence) || 1, cover_url: coverUrl.trim(), blocks, ...(document ? { expected_version: document.current_version } : {}) };
      const savedDocument = documentId ? await browserApi<Document>(`/api/backend/admin/documents/${documentId}`, { method: "PATCH", body: JSON.stringify(payload) }) : await browserApi<Document>("/api/backend/admin/documents", { method: "POST", body: JSON.stringify(payload) });
      setDocument(savedDocument);
      setStatus(savedDocument.status);
      setSaved(true);
      if (!documentId) router.replace(`/admin/documents/${savedDocument.id}/edit`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  if (!ready) return <div className="mx-auto max-w-7xl animate-pulse px-5 py-8 lg:px-8"><div className="h-8 w-40 rounded bg-[var(--surface-muted)]" /><div className="mt-8 h-[560px] rounded bg-[var(--surface-muted)]" /></div>;

  return (
    <div className="mx-auto max-w-7xl px-5 py-6 lg:px-8">
      <header className="flex flex-wrap items-center justify-between gap-4 border-b border-[var(--border)] pb-5"><div><Link href="/admin/documents" className="inline-flex items-center gap-1 text-sm text-[var(--muted)] hover:text-[var(--foreground)]"><ArrowLeft size={15} aria-hidden="true" /> 文档</Link><h1 className="mt-2 text-2xl font-semibold">{heading}</h1></div><div className="flex items-center gap-2"><span className="text-xs text-[var(--faint)]">{saving ? "保存中..." : saved ? "已保存" : ""}</span><Button type="button" onClick={() => save("draft")} disabled={saving}><Save size={16} aria-hidden="true" /> 保存草稿</Button><Button type="button" variant="brand" onClick={() => save("published")} disabled={saving}><Check size={16} aria-hidden="true" /> 发布</Button></div></header>
      {error && <p role="alert" className="mt-5 rounded border border-[#efc7c4] bg-[#fff5f4] px-3 py-2 text-sm text-[var(--danger)]">{error}</p>}
      <div className="mt-7 grid gap-8 lg:grid-cols-[minmax(0,1fr)_280px]">
        <section className="min-w-0 space-y-5"><TextField id="title" label="标题" value={title} onChange={(event) => setTitle(event.target.value)} placeholder="输入一眼能理解的标题" required /><TextArea id="summary" label="摘要" value={summary} onChange={(event) => setSummary(event.target.value)} placeholder="用一两句话说明文章解决什么问题" rows={3} /><div className="overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)]"><div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3"><h2 className="text-sm font-semibold">正文</h2><span className="text-xs text-[var(--faint)]">BlockNote · Markdown projection</span></div><div className="min-h-[560px]"><BlockNoteView editor={editor} theme="light" /></div></div></section>
        <aside className="space-y-5 lg:sticky lg:top-6 lg:self-start"><div className="space-y-5 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] p-4"><TextField id="slug" label="Slug" value={slug} onChange={(event) => setSlug(event.target.value)} placeholder="article-slug" required hint="用于公开 URL，保存后请谨慎修改" /><label className="flex flex-col gap-1.5 text-sm"><span className="font-medium">分类</span><select value={categoryId} onChange={(event) => setCategoryId(event.target.value)} className="min-h-10 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] px-3 text-sm" required><option value="">选择分类</option>{categories.map((category) => <option key={category.id} value={category.id}>{category.name}</option>)}</select></label><div><p className="mb-2 text-sm font-medium">标签</p><div className="flex flex-wrap gap-2">{tags.map((tag) => { const active = selectedTags.includes(tag.id); return <button key={tag.id} type="button" onClick={() => setSelectedTags((current) => active ? current.filter((id) => id !== tag.id) : [...current, tag.id])} className={`rounded px-2 py-1.5 text-xs ${active ? "bg-[var(--brand)] text-white" : "bg-[var(--surface-muted)] text-[var(--muted)]"}`}>{tag.name}</button>; })}</div></div><TextField id="cover_url" label="封面地址" value={coverUrl} onChange={(event) => setCoverUrl(event.target.value)} placeholder="/api/v1/assets/..." /><label className="flex cursor-pointer items-center gap-2 rounded border border-dashed border-[var(--border-strong)] px-3 py-2 text-xs text-[var(--muted)]"><CloudUpload size={15} aria-hidden="true" />上传图片<input type="file" accept="image/*" className="sr-only" onChange={async (event) => { const file = event.target.files?.[0]; if (!file) return; try { setCoverUrl(await uploadFile(file)); } catch (reason) { setError(reason instanceof Error ? reason.message : "上传失败"); } }} /></label><label className="flex flex-col gap-1.5 text-sm"><span className="font-medium">状态</span><select value={status} onChange={(event) => setStatus(event.target.value as "draft" | "published")} className="min-h-10 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] px-3 text-sm"><option value="draft">草稿</option><option value="published">已发布</option></select></label><div className="grid grid-cols-2 gap-3"><label className="flex flex-col gap-1.5 text-sm"><span className="font-medium">来源</span><select id="source" value={source} onChange={(event) => setSource(event.target.value)} className="min-h-10 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"><option value="manual">手动创作</option><option value="import">导入</option><option value="agent">Agent</option></select></label><TextField id="confidence" label="可信度" type="number" min="0" max="1" step="0.1" value={confidence} onChange={(event) => setConfidence(event.target.value)} /></div></div></aside>
      </div>
    </div>
  );
}
