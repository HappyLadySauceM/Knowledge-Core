"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { FormEvent, useState } from "react";
import { UserPlus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TextField } from "@/components/ui/text-field";
import type { ApiEnvelope, TokenResponse } from "@/lib/api/types";

export function RegisterForm() {
  const router = useRouter();
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    const form = new FormData(event.currentTarget);
    const password = String(form.get("password") ?? "");
    if (password !== form.get("confirm_password")) return setError("两次输入的密码不一致");
    setPending(true);
    try {
      const response = await fetch("/api/session/register", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: form.get("username"), email: form.get("email"), password }),
      });
      const payload = await response.json() as ApiEnvelope<TokenResponse>;
      if (!response.ok || payload.code >= 400) throw new Error(payload.message || "注册失败");
      router.replace("/profile");
      router.refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "注册失败，请稍后重试");
    } finally {
      setPending(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      <TextField id="username" name="username" label="用户名" autoComplete="username" required minLength={3} placeholder="3-32 个字符" />
      <TextField id="email" name="email" type="email" label="邮箱" autoComplete="email" placeholder="name@example.com" />
      <TextField id="password" name="password" type="password" label="密码" autoComplete="new-password" required minLength={8} placeholder="至少 8 个字符" />
      <TextField id="confirm_password" name="confirm_password" type="password" label="确认密码" autoComplete="new-password" required placeholder="再次输入密码" />
      {error && <p role="alert" className="rounded-[var(--radius)] border border-[#efc7c4] bg-[#fff5f4] px-3 py-2 text-sm text-[var(--danger)]">{error}</p>}
      <Button type="submit" variant="brand" size="lg" className="w-full" disabled={pending}><UserPlus size={17} aria-hidden="true" />{pending ? "正在创建..." : "创建账号"}</Button>
      <p className="text-center text-sm text-[var(--muted)]">已有账号？ <Link href="/login" className="font-medium text-[var(--brand)] hover:underline">直接登录</Link></p>
    </form>
  );
}
