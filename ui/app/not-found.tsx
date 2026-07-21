import Link from "next/link";
import { ArrowLeft } from "lucide-react";

export default function NotFoundPage() {
  return (
    <main className="mx-auto flex min-h-[60vh] max-w-xl flex-col items-center justify-center px-5 text-center">
      <p className="font-mono text-sm text-[var(--brand)]">404</p>
      <h1 className="mt-3 text-3xl font-semibold">没有找到这篇内容</h1>
      <p className="mt-3 text-sm leading-6 text-[var(--muted)]">它可能还没有发布，或链接已经发生变化。</p>
      <Link href="/knowledge" className="mt-6 inline-flex items-center gap-2 rounded-[var(--radius)] bg-[var(--brand)] px-4 py-2.5 text-sm font-medium text-white hover:bg-[var(--brand-hover)]"><ArrowLeft size={16} aria-hidden="true" /> 返回文库</Link>
    </main>
  );
}
