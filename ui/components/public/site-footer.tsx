"use client";

import Link from "next/link";
import { Github } from "lucide-react";
import { usePathname } from "next/navigation";
import { siteConfig } from "@/lib/site";

export function SiteFooter() {
  const pathname = usePathname();
  if (pathname.startsWith("/admin")) return null;

  return (
    <footer className="mt-20 border-t border-[var(--border)] bg-[var(--surface)]">
      <div className="mx-auto flex max-w-7xl flex-col gap-5 px-5 py-8 text-sm text-[var(--faint)] sm:flex-row sm:items-center sm:justify-between lg:px-8">
        <p>{siteConfig.name} · 用 AI 构建个人知识体系</p>
        <div className="flex items-center gap-4">
          <Link href="/about" className="hover:text-[var(--foreground)]">关于</Link>
          <a href={siteConfig.social.github} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 hover:text-[var(--foreground)]"><Github size={15} aria-hidden="true" /> GitHub</a>
        </div>
      </div>
    </footer>
  );
}
