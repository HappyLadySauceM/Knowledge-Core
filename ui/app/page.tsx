import Image from "next/image";
import Link from "next/link";
import { ArrowRight, BookOpen, Compass, Layers3 } from "lucide-react";
import { DocumentCard } from "@/components/public/document-card";
import { listPublicDocuments } from "@/lib/api/public-data";
import { siteConfig } from "@/lib/site";

export default async function HomePage() {
  const documents = (await listPublicDocuments({ pageSize: 5 })).items;
  return (
    <main>
      <section className="relative flex min-h-[520px] max-h-[680px] h-[68vh] items-end overflow-hidden bg-[#171b22] text-white">
        <Image src="/images/cover-deep-learning.jpg" alt="知识节点与连接构成的可视化网络" fill loading="eager" fetchPriority="high" sizes="100vw" className="object-cover object-center opacity-70" />
        <div className="absolute inset-0 bg-black/35" aria-hidden="true" />
        <div className="relative mx-auto w-full max-w-7xl px-5 pb-16 lg:px-8 lg:pb-20">
          <div className="max-w-3xl">
            <p className="mb-4 flex items-center gap-2 text-sm font-medium text-white/80"><BookOpen size={17} aria-hidden="true" /> 个人知识空间</p>
            <h1 className="font-[var(--font-heading)] text-4xl font-semibold leading-tight sm:text-5xl lg:text-6xl">{siteConfig.name}</h1>
            <p className="mt-5 max-w-2xl text-base leading-7 text-white/85 sm:text-lg">{siteConfig.description}</p>
            <div className="mt-8 flex flex-wrap gap-3">
              <Link href="/knowledge" className="inline-flex items-center gap-2 rounded-[var(--radius)] bg-white px-4 py-2.5 text-sm font-semibold text-[#1d232b] hover:bg-[#f0f2f5]">浏览文库 <ArrowRight size={16} aria-hidden="true" /></Link>
              <Link href="/about" className="inline-flex items-center rounded-[var(--radius)] border border-white/40 bg-black/20 px-4 py-2.5 text-sm font-medium text-white hover:bg-black/35">关于这里</Link>
            </div>
          </div>
        </div>
      </section>

      <section className="border-b border-[var(--border)] bg-[var(--surface)]">
        <div className="mx-auto grid max-w-7xl gap-5 px-5 py-8 sm:grid-cols-3 lg:px-8">
          <div className="flex items-start gap-3"><Compass className="mt-0.5 text-[#087f6d]" size={20} aria-hidden="true" /><div><h2 className="text-sm font-semibold">持续探索</h2><p className="mt-1 text-sm leading-6 text-[var(--muted)]">记录还没有定论的问题和正在形成的判断。</p></div></div>
          <div className="flex items-start gap-3"><Layers3 className="mt-0.5 text-[#b35c1e]" size={20} aria-hidden="true" /><div><h2 className="text-sm font-semibold">结构化整理</h2><p className="mt-1 text-sm leading-6 text-[var(--muted)]">让零散材料通过分类和标签形成可检索的脉络。</p></div></div>
          <div className="flex items-start gap-3"><BookOpen className="mt-0.5 text-[var(--brand)]" size={20} aria-hidden="true" /><div><h2 className="text-sm font-semibold">公开写作</h2><p className="mt-1 text-sm leading-6 text-[var(--muted)]">用清晰的表达检验理解，也为下一次思考留下入口。</p></div></div>
        </div>
      </section>

      <section className="mx-auto max-w-7xl px-5 py-16 lg:px-8">
        <div className="mb-8 flex items-end justify-between gap-5">
          <div><p className="text-sm font-medium text-[var(--brand)]">最近更新</p><h2 className="mt-2 font-[var(--font-heading)] text-2xl font-semibold sm:text-3xl">从最新的思考开始</h2></div>
          <Link href="/knowledge" className="hidden items-center gap-1 text-sm font-medium text-[var(--muted)] hover:text-[var(--foreground)] sm:flex">全部文章 <ArrowRight size={16} aria-hidden="true" /></Link>
        </div>
        {documents.length ? <div className="grid gap-5 md:grid-cols-2 lg:grid-cols-3">{documents.map((document, index) => <DocumentCard key={document.id} document={document} featured={index === 0} />)}</div> : <p className="border-y border-[var(--border)] py-12 text-center text-sm text-[var(--muted)]">还没有公开文章。</p>}
      </section>
    </main>
  );
}
