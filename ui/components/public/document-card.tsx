import Image from "next/image";
import Link from "next/link";
import { ArrowUpRight } from "lucide-react";
import type { Document } from "@/lib/api/types";
import { fallbackCovers } from "@/lib/site";
import { formatDate, formatWordCount } from "@/lib/format";

export function DocumentCard({ document, featured = false }: { document: Document; featured?: boolean }) {
  const cover = document.cover_url || fallbackCovers[document.id % fallbackCovers.length];
  return (
    <article className={`group overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] transition hover:-translate-y-0.5 hover:border-[var(--border-strong)] hover:shadow-[0_8px_24px_rgba(29,35,43,0.07)] ${featured ? "md:col-span-2" : ""}`}>
      <Link href={`/articles/${document.slug}`} className={featured ? "grid md:grid-cols-[1.15fr_1fr]" : "block"}>
        <div className={`relative overflow-hidden bg-[var(--surface-muted)] ${featured ? "aspect-[16/10] md:aspect-auto md:min-h-64" : "aspect-[16/9]"}`}>
          <Image src={cover} alt="" fill sizes={featured ? "(max-width: 768px) 100vw, 55vw" : "(max-width: 768px) 100vw, 33vw"} className="object-cover transition duration-500 group-hover:scale-[1.03]" />
        </div>
        <div className="flex min-h-48 flex-col p-5">
          <div className="mb-3 flex items-center justify-between gap-3 text-xs text-[var(--faint)]">
            <span>{document.category?.name ?? "未分类"}</span>
            <span>{formatDate(document.published_at)}</span>
          </div>
          <h2 className={`${featured ? "text-2xl" : "text-lg"} font-semibold leading-tight text-[var(--foreground)]`}>{document.title}</h2>
          <p className="mt-3 line-clamp-3 text-sm leading-6 text-[var(--muted)]">{document.summary}</p>
          <div className="mt-auto flex items-end justify-between gap-3 pt-5">
            <div className="flex flex-wrap gap-1.5">
              {document.tags.slice(0, 3).map((tag) => <span key={tag.slug} className="rounded bg-[var(--surface-subtle)] px-2 py-1 text-xs text-[var(--muted)]">{tag.name}</span>)}
            </div>
            <span className="flex shrink-0 items-center gap-1 text-xs text-[var(--faint)]"><span className="hidden sm:inline">{formatWordCount(document.word_count)} 字</span><ArrowUpRight size={15} aria-hidden="true" /></span>
          </div>
        </div>
      </Link>
    </article>
  );
}
