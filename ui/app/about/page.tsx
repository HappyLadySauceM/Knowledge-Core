import type { Metadata } from "next";
import Image from "next/image";
import { ArrowUpRight, Github, Mail } from "lucide-react";
import { siteConfig } from "@/lib/site";

export const metadata: Metadata = { title: "关于", description: `关于 ${siteConfig.author} 和这个知识库。` };

export default function AboutPage() {
  return (
    <main>
      <section className="relative flex min-h-[430px] items-end overflow-hidden bg-[#171b22] text-white">
        <Image src="/images/author-portrait.jpg" alt="开发者在个人工作区" fill priority sizes="100vw" className="object-cover object-center opacity-65" />
        <div className="absolute inset-0 bg-black/40" aria-hidden="true" />
        <div className="relative mx-auto w-full max-w-5xl px-5 pb-14 pt-24">
          <p className="text-sm font-medium text-white/75">About</p>
          <h1 className="mt-3 font-[var(--font-heading)] text-4xl font-semibold sm:text-5xl">{siteConfig.author}</h1>
          <p className="mt-4 max-w-2xl text-lg leading-8 text-white/85">写代码、读书，也持续整理那些值得被再次想起的事。</p>
        </div>
      </section>

      <section className="mx-auto grid max-w-5xl gap-10 px-5 py-14 md:grid-cols-[220px_1fr] md:py-20">
        <div><Image src="/images/author-portrait.jpg" alt={`${siteConfig.author} 的头像`} width={220} height={220} className="aspect-square w-full max-w-[220px] rounded-[var(--radius)] object-cover" /><p className="mt-4 text-sm text-[var(--muted)]">{siteConfig.authorRole}</p></div>
        <div className="max-w-2xl text-base leading-8 text-[var(--muted)]">
          <h2 className="font-[var(--font-heading)] text-2xl font-semibold text-[var(--foreground)]">为什么建立 Knowledge Core</h2>
          <p className="mt-5">信息容易获得，但理解需要时间。这个站点用于保存学习过程中的推理、实践和修正，让它们不只是散落在收藏夹与临时笔记里。</p>
          <p className="mt-4">这里的文章会持续更新。技术记录关注能够复用的方法，阅读笔记保留影响判断的观点，项目复盘则尽量把结果背后的取舍说清楚。</p>
          <blockquote className="my-8 border-l-2 border-[var(--brand)] pl-5 text-lg text-[var(--foreground)]">写作不是整理完成后的最后一步，它本身就是理解的一部分。</blockquote>
          <div className="flex flex-wrap gap-3 border-t border-[var(--border)] pt-7">
            <a href={siteConfig.social.github} target="_blank" rel="noreferrer" className="inline-flex items-center gap-2 rounded-[var(--radius)] border border-[var(--border)] px-3 py-2 text-sm font-medium text-[var(--foreground)] hover:bg-[var(--surface-subtle)]"><Github size={16} aria-hidden="true" /> GitHub <ArrowUpRight size={14} aria-hidden="true" /></a>
            <a href="mailto:hello@example.com" className="inline-flex items-center gap-2 rounded-[var(--radius)] border border-[var(--border)] px-3 py-2 text-sm font-medium text-[var(--foreground)] hover:bg-[var(--surface-subtle)]"><Mail size={16} aria-hidden="true" /> 联系我</a>
          </div>
        </div>
      </section>
    </main>
  );
}
