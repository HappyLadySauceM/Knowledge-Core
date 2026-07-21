"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { FormEvent, useState } from "react";
import { LogIn } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TextField } from "@/components/ui/text-field";
import type { ApiEnvelope, TokenResponse } from "@/lib/api/types";

export function LoginForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setPending(true);
    const form = new FormData(event.currentTarget);
    try {
      const response = await fetch("/api/session/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: form.get("username"), password: form.get("password") }),
      });
      const payload = await response.json() as ApiEnvelope<TokenResponse>;
      if (!response.ok || payload.code >= 400) throw new Error(payload.message || "登录失败");
      const requested = searchParams.get("next");
      const destination = requested?.startsWith("/") && !requested.startsWith("//") ? requested : payload.data.user.role === "admin" ? "/admin" : "/profile";
      router.replace(destination);
      router.refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "登录失败，请稍后重试");
    } finally {
      setPending(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      <TextField id="username" name="username" label="用户名" autoComplete="username" required placeholder="输入用户名" />
      <TextField id="password" name="password" type="password" label="密码" autoComplete="current-password" required placeholder="输入密码" />
      {error && <p role="alert" className="rounded-[var(--radius)] border border-[#efc7c4] bg-[#fff5f4] px-3 py-2 text-sm text-[var(--danger)]">{error}</p>}
      <Button type="submit" variant="brand" size="lg" className="w-full" disabled={pending}><LogIn size={17} aria-hidden="true" />{pending ? "正在登录..." : "登录"}</Button>
      <p className="text-center text-sm text-[var(--muted)]">还没有账号？ <Link href="/register" className="font-medium text-[var(--brand)] hover:underline">创建账号</Link></p>
    </form>
  );
}
