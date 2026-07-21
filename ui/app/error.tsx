"use client";

import { RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";

export default function ErrorPage({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <main className="mx-auto flex min-h-[60vh] max-w-xl flex-col items-center justify-center px-5 text-center">
      <p className="text-sm font-medium text-[var(--danger)]">页面加载失败</p>
      <h1 className="mt-3 text-2xl font-semibold">暂时无法读取内容</h1>
      <p className="mt-3 text-sm leading-6 text-[var(--muted)]">请检查服务连接后重试。</p>
      <Button className="mt-6" onClick={reset}><RotateCcw size={16} aria-hidden="true" /> 重新加载</Button>
    </main>
  );
}
