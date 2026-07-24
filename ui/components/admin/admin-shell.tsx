"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { BookOpen, Files, LayoutDashboard, LogOut, Menu, Tags, Users, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { ApiEnvelope, User } from "@/lib/api/types";
import { clsx } from "clsx";

const links = [
  { href: "/admin", label: "概览", icon: LayoutDashboard },
  { href: "/admin/documents", label: "文档", icon: Files },
  { href: "/admin/taxonomy", label: "分类与标签", icon: Tags },
  { href: "/admin/users", label: "用户", icon: Users },
];

export function AdminShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [ready, setReady] = useState(false);
  const [open, setOpen] = useState(false);
  const isChangePassword = pathname === "/admin/change-password";

  useEffect(() => {
    fetch("/api/session/me", { cache: "no-store" }).then(async (response) => {
      if (!response.ok) return router.replace(`/login?next=${encodeURIComponent(pathname)}`);
      const payload = await response.json() as ApiEnvelope<User>;
      if (payload.data.role !== "admin") return router.replace("/profile");
      if (payload.data.must_change_password && !isChangePassword) return router.replace("/admin/change-password");
      if (!payload.data.must_change_password && isChangePassword) return router.replace("/admin");
      setUser(payload.data);
      setReady(true);
    }).catch(() => router.replace(`/login?next=${encodeURIComponent(pathname)}`));
  }, [pathname, router, isChangePassword]);

  async function logout() {
    await fetch("/api/session/logout", { method: "POST" });
    router.replace("/");
    router.refresh();
  }

  if (!ready) return <div className="grid min-h-screen place-items-center bg-[var(--background)] text-sm text-[var(--muted)]">正在验证管理权限...</div>;

  if (isChangePassword) return <main className="min-w-0">{children}</main>;

  return (
    <div className="min-h-screen bg-[var(--background)] lg:grid lg:grid-cols-[232px_1fr]">
      <header className="sticky top-0 z-40 flex h-14 items-center justify-between border-b border-[var(--border)] bg-[var(--surface)] px-4 lg:hidden">
        <Link href="/admin" className="flex items-center gap-2 text-sm font-semibold"><BookOpen size={18} className="text-[var(--brand)]" aria-hidden="true" /> Knowledge Core</Link>
        <button type="button" className="grid size-9 place-items-center rounded hover:bg-[var(--surface-subtle)]" aria-label={open ? "关闭管理菜单" : "打开管理菜单"} aria-expanded={open} onClick={() => setOpen((value) => !value)}>{open ? <X size={19} /> : <Menu size={19} />}</button>
      </header>
      <aside className={clsx("fixed inset-y-0 left-0 z-50 flex w-[232px] flex-col border-r border-[var(--border)] bg-[var(--surface)] p-3 transition-transform lg:sticky lg:top-0 lg:h-screen lg:translate-x-0", open ? "translate-x-0" : "-translate-x-full")}>
        <Link href="/admin" className="flex h-12 items-center gap-2 px-2 text-sm font-semibold"><span className="grid size-8 place-items-center rounded bg-[var(--brand)] text-white"><BookOpen size={17} aria-hidden="true" /></span> Knowledge Core</Link>
        <nav className="mt-5 space-y-1" aria-label="管理导航">{links.map(({ href, label, icon: Icon }) => { const active = href === "/admin" ? pathname === href : pathname.startsWith(href); return <Link key={href} href={href} onClick={() => setOpen(false)} className={clsx("flex min-h-10 items-center gap-3 rounded px-3 text-sm", active ? "bg-[var(--brand-soft)] font-medium text-[var(--brand)]" : "text-[var(--muted)] hover:bg-[var(--surface-subtle)] hover:text-[var(--foreground)]")}><Icon size={17} aria-hidden="true" />{label}</Link>; })}</nav>
        <div className="mt-auto border-t border-[var(--border)] pt-3"><div className="px-3 py-2"><p className="truncate text-sm font-medium">{user?.username}</p><p className="mt-0.5 text-xs text-[var(--faint)]">管理员</p></div><button type="button" onClick={logout} className="flex min-h-9 w-full items-center gap-3 rounded px-3 text-sm text-[var(--muted)] hover:bg-[var(--surface-subtle)] hover:text-[var(--foreground)]"><LogOut size={16} aria-hidden="true" />退出登录</button></div>
      </aside>
      {open && <button type="button" aria-label="关闭管理菜单" className="fixed inset-0 z-40 bg-black/30 lg:hidden" onClick={() => setOpen(false)} />}
      <main className="min-w-0">{children}</main>
    </div>
  );
}
