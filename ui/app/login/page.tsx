import type { Metadata } from "next";
import { Suspense } from "react";
import { BookOpen } from "lucide-react";
import { LoginForm } from "@/components/auth/login-form";

export const metadata: Metadata = { title: "登录" };

export default function LoginPage() {
  return (
    <main className="grid min-h-[calc(100vh-64px)] place-items-center px-5 py-12">
      <section className="w-full max-w-md rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] p-6 shadow-[0_12px_36px_rgba(29,35,43,0.08)] sm:p-8">
        <div className="mb-7"><span className="grid size-10 place-items-center rounded-[var(--radius)] bg-[var(--brand)] text-white"><BookOpen size={19} aria-hidden="true" /></span><h1 className="mt-5 text-2xl font-semibold">欢迎回来</h1><p className="mt-2 text-sm text-[var(--muted)]">登录后继续管理你的知识库。</p></div>
        <Suspense fallback={<p className="text-sm text-[var(--muted)]">正在加载...</p>}><LoginForm /></Suspense>
      </section>
    </main>
  );
}
