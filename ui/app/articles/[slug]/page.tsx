import type { Metadata } from "next";
import Image from "next/image";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, CalendarDays, Clock3 } from "lucide-react";
import { MarkdownContent } from "@/components/public/markdown-content";
import { findPublicDocument } from "@/lib/api/public-data";
import { formatDate, formatSource, formatWordCount } from "@/lib/format";
import { fallbackCovers, siteConfig } from "@/lib/site";

type PageProps = { params: Promise<{ slug: string }> };

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { slug } = await params;
  const document = await findPublicDocument(slug);
  if (!document) return { title: "文章不存在" };
  return {
    title: document.title,
    description: document.summary,
    openGraph: { title: document.title, description: document.summary, type: "article", publishedTime: document.published_at },
  };
}

export default async function ArticlePage({ params }: PageProps) {
  const { slug } = await params;
  const document = await findPublicDocument(slug);
  if (!document) notFound();
  const cover = document.cover_url || fallbackCovers[document.id % fallbackCovers.length];
  const readingMinutes = Math.max(1, Math.ceil(document.word_count / 450));
  return (
    <main>
      <article>
        <header className="mx-auto max-w-4xl px-5 pb-9 pt-10 sm:pt-14">
          <Link href="/knowledge" className="inline-flex items-center gap-1.5 text-sm text-[var(--muted)] hover:text-[var(--foreground)]"><ArrowLeft size={16} aria-hidden="true" /> 返回文库</Link>
          <div className="mt-8 flex flex-wrap items-center gap-2 text-sm text-[var(--brand)]"><Link href={`/knowledge?category=${encodeURIComponent(document.category?.slug ?? "")}`} className="font-medium hover:underline">{document.category?.name ?? "未分类"}</Link><span className="text-[var(--border-strong)]">/</span><span>{formatSource(document.source)}</span></div>
          <h1 className="mt-4 font-[var(--font-heading)] text-3xl font-semibold leading-tight sm:text-5xl">{document.title}</h1>
          <p className="mt-5 max-w-3xl text-base leading-7 text-[var(--muted)] sm:text-lg">{document.summary}</p>
          <div className="mt-6 flex flex-wrap items-center gap-x-5 gap-y-2 text-sm text-[var(--faint)]">
            <span>{siteConfig.author}</span>
            <span className="inline-flex items-center gap-1.5"><CalendarDays size={15} aria-hidden="true" /> {formatDate(document.published_at)}</span>
            <span className="inline-flex items-center gap-1.5"><Clock3 size={15} aria-hidden="true" /> 约 {readingMinutes} 分钟</span>
            <span>{formatWordCount(document.word_count)} 字</span>
          </div>
        </header>

        <div className="relative mx-auto aspect-[16/7] max-h-[560px] min-h-64 w-full overflow-hidden bg-[var(--surface-muted)]">
          <Image src={cover} alt="" fill priority sizes="100vw" className="object-cover" />
        </div>

        <div className="mx-auto grid max-w-5xl gap-10 px-5 py-10 md:grid-cols-[minmax(0,720px)_1fr] md:py-14">
          <MarkdownContent content={document.content ?? ""} />
          <aside className="order-first border-b border-[var(--border)] pb-6 md:order-none md:border-b-0 md:border-l md:pl-6" aria-label="文章信息">
            <h2 className="text-sm font-semibold">标签</h2>
            <div className="mt-3 flex flex-wrap gap-2">{document.tags.map((tag) => <Link key={tag.slug} href={`/knowledge?tag=${encodeURIComponent(tag.slug)}`} className="rounded bg-[var(--surface-muted)] px-2 py-1.5 text-xs text-[var(--muted)] hover:text-[var(--foreground)]">{tag.name}</Link>)}</div>
          </aside>
        </div>
      </article>
    </main>
  );
}
