"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { BookOpen, Menu, Search, X } from "lucide-react";
import { useState } from "react";
import { siteConfig } from "@/lib/site";

export function SiteHeader() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  if (pathname.startsWith("/admin")) return null;

  return (
    <header className="sticky top-0 z-40 border-b border-[var(--border)] bg-[color:var(--surface)]/95 backdrop-blur">
      <div className="mx-auto flex h-16 max-w-7xl items-center justify-between gap-6 px-5 lg:px-8">
        <Link href="/" className="flex items-center gap-2 font-[var(--font-heading)] text-base font-semibold tracking-normal">
          <span className="grid size-8 place-items-center rounded-lg bg-[var(--brand)] text-white"><BookOpen size={17} aria-hidden="true" /></span>
          {siteConfig.name}
        </Link>
        <nav className="hidden items-center gap-7 text-sm text-[var(--muted)] md:flex" aria-label="主导航">
          <Link className="transition-colors hover:text-[var(--foreground)]" href="/">首页</Link>
          <Link className="transition-colors hover:text-[var(--foreground)]" href="/knowledge">文库</Link>
          <Link className="transition-colors hover:text-[var(--foreground)]" href="/about">关于</Link>
        </nav>
        <div className="hidden items-center gap-2 md:flex">
          <Link href="/knowledge" aria-label="搜索文章" className="grid size-9 place-items-center rounded-lg text-[var(--muted)] hover:bg-[var(--surface-subtle)] hover:text-[var(--foreground)]">
            <Search size={18} aria-hidden="true" />
          </Link>
          <Link href="/login" className="rounded-[var(--radius)] px-3 py-2 text-sm font-medium text-[var(--muted)] hover:bg-[var(--surface-subtle)] hover:text-[var(--foreground)]">登录</Link>
          <Link href="/register" className="rounded-[var(--radius)] bg-[var(--brand)] px-3 py-2 text-sm font-medium text-white hover:bg-[var(--brand-hover)]">注册</Link>
        </div>
        <button className="grid size-10 place-items-center rounded-lg text-[var(--muted)] hover:bg-[var(--surface-subtle)] md:hidden" type="button" aria-label={open ? "关闭菜单" : "打开菜单"} aria-expanded={open} onClick={() => setOpen((value) => !value)}>
          {open ? <X size={20} aria-hidden="true" /> : <Menu size={20} aria-hidden="true" />}
        </button>
      </div>
      {open && (
        <nav className="border-t border-[var(--border)] bg-[var(--surface)] px-5 py-3 md:hidden" aria-label="移动端主导航">
          <div className="mx-auto flex max-w-7xl flex-col gap-1 text-sm">
            <Link className="rounded-lg px-3 py-3 hover:bg-[var(--surface-subtle)]" href="/" onClick={() => setOpen(false)}>首页</Link>
            <Link className="rounded-lg px-3 py-3 hover:bg-[var(--surface-subtle)]" href="/knowledge" onClick={() => setOpen(false)}>文库</Link>
            <Link className="rounded-lg px-3 py-3 hover:bg-[var(--surface-subtle)]" href="/about" onClick={() => setOpen(false)}>关于</Link>
            <div className="mt-2 flex gap-2 border-t border-[var(--border)] pt-3">
              <Link className="flex-1 rounded-lg border border-[var(--border)] px-3 py-2 text-center" href="/login" onClick={() => setOpen(false)}>登录</Link>
              <Link className="flex-1 rounded-lg bg-[var(--brand)] px-3 py-2 text-center text-white" href="/register" onClick={() => setOpen(false)}>注册</Link>
            </div>
          </div>
        </nav>
      )}
    </header>
  );
}
