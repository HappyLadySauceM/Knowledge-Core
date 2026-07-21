import type { Metadata } from "next";
import Image from "next/image";
import Link from "next/link";
import { DocumentCard } from "@/components/public/document-card";
import { SearchForm } from "@/components/public/search-form";
import { listPublicCategories, listPublicDocuments, listPublicTags } from "@/lib/api/public-data";
import { siteConfig } from "@/lib/site";

export const metadata: Metadata = { title: "文库", description: "按分类、标签或关键词浏览 Knowledge Core 中的公开文章。" };

type SearchParams = Promise<{ q?: string; category?: string; tag?: string }>;

export default async function KnowledgePage({ searchParams }: { searchParams: SearchParams }) {
  const query = await searchParams;
  const [documents, categories, tags] = await Promise.all([
    listPublicDocuments({ q: query.q, category: query.category, tag: query.tag, pageSize: 24 }),
    listPublicCategories(),
    listPublicTags(),
  ]);
  const hasFilter = Boolean(query.q || query.category || query.tag);
  return (
    <main className="mx-auto max-w-7xl px-5 py-10 lg:px-8 lg:py-14">
      <header className="grid gap-7 border-b border-[var(--border)] pb-9 md:grid-cols-[1fr_minmax(280px,420px)] md:items-end">
        <div><p className="text-sm font-medium text-[var(--brand)]">Knowledge Library</p><h1 className="mt-2 font-[var(--font-heading)] text-3xl font-semibold sm:text-4xl">文库</h1><p className="mt-3 max-w-xl leading-7 text-[var(--muted)]">按主题浏览长期积累的文章，或直接搜索你关心的概念。</p></div>
        <SearchForm defaultValue={query.q} />
      </header>

      <div className="grid gap-10 pt-9 lg:grid-cols-[minmax(0,1fr)_250px]">
        <section aria-labelledby="document-list-title">
          <div className="mb-5 flex items-center justify-between gap-3"><h2 id="document-list-title" className="text-sm font-semibold">{hasFilter ? "筛选结果" : "全部文章"}</h2><span className="text-sm text-[var(--faint)]">{documents.total ?? documents.items.length} 篇</span></div>
          {documents.items.length ? <div className="grid gap-5 md:grid-cols-2">{documents.items.map((document) => <DocumentCard key={document.id} document={document} />)}</div> : <div className="border-y border-[var(--border)] py-16 text-center"><p className="font-medium">没有找到匹配的文章</p><p className="mt-2 text-sm text-[var(--muted)]">尝试更短的关键词或清除筛选条件。</p><Link href="/knowledge" className="mt-5 inline-flex rounded-[var(--radius)] border border-[var(--border)] px-3 py-2 text-sm hover:bg-[var(--surface-subtle)]">查看全部</Link></div>}
        </section>

        <aside className="space-y-8 lg:sticky lg:top-24 lg:self-start" aria-label="文库筛选">
          <div className="flex items-center gap-3"><Image src="/images/author-portrait.jpg" alt={`${siteConfig.author} 的头像`} width={48} height={48} className="size-12 rounded-full object-cover" /><div><p className="text-sm font-semibold">{siteConfig.author}</p><p className="mt-0.5 text-xs text-[var(--muted)]">{siteConfig.authorRole}</p></div></div>
          <div><h2 className="mb-3 text-sm font-semibold">分类</h2><div className="space-y-1"><Link href="/knowledge" className={`flex items-center justify-between rounded px-2 py-2 text-sm ${!query.category ? "bg-[var(--brand-soft)] text-[var(--brand)]" : "text-[var(--muted)] hover:bg-[var(--surface-subtle)]"}`}><span>全部</span><span>{documents.total ?? documents.items.length}</span></Link>{categories.map((category) => <Link key={category.slug} href={`/knowledge?category=${encodeURIComponent(category.slug)}`} className={`flex items-center justify-between rounded px-2 py-2 text-sm ${query.category === category.slug ? "bg-[var(--brand-soft)] text-[var(--brand)]" : "text-[var(--muted)] hover:bg-[var(--surface-subtle)]"}`}><span>{category.name}</span><span>{category.document_count}</span></Link>)}</div></div>
          <div><h2 className="mb-3 text-sm font-semibold">标签</h2><div className="flex flex-wrap gap-2">{tags.map((tag) => <Link key={tag.slug} href={`/knowledge?tag=${encodeURIComponent(tag.slug)}`} className={`rounded px-2 py-1.5 text-xs ${query.tag === tag.slug ? "bg-[var(--brand)] text-white" : "bg-[var(--surface-muted)] text-[var(--muted)] hover:text-[var(--foreground)]"}`}>{tag.name}</Link>)}</div></div>
        </aside>
      </div>
    </main>
  );
}
