"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { KeyRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TextField } from "@/components/ui/text-field";
import type { ApiEnvelope } from "@/lib/api/types";

export function AdminChangePasswordForm() {
  const router = useRouter();
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setPending(true);
    const form = new FormData(event.currentTarget);
    const newPassword = String(form.get("new_password") ?? "");
    const confirmPassword = String(form.get("confirm_password") ?? "");
    if (newPassword !== confirmPassword) {
      setError("两次输入的新密码不一致");
      setPending(false);
      return;
    }
    try {
      const response = await fetch("/api/session/password", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          old_password: form.get("old_password"),
          new_password: newPassword,
        }),
      });
      const payload = await response.json() as ApiEnvelope<unknown>;
      if (!response.ok || payload.code >= 400) throw new Error(payload.message || "密码更新失败");
      await fetch("/api/session/logout", { method: "POST" });
      router.replace("/login?next=/admin&message=password_changed");
      router.refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "密码更新失败，请稍后重试");
    } finally {
      setPending(false);
    }
  }

  return (
    <form onSubmit={submit} className="mx-auto w-full max-w-md space-y-5 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] p-6 shadow-sm">
      <div>
        <p className="text-sm font-medium text-[var(--brand)]">安全设置</p>
        <h1 className="mt-1 text-2xl font-semibold">修改初始密码</h1>
        <p className="mt-2 text-sm text-[var(--muted)]">首次登录管理员账号必须设置新密码后才能进入后台。</p>
      </div>
      <TextField id="old_password" name="old_password" type="password" label="当前密码" autoComplete="current-password" required />
      <TextField id="new_password" name="new_password" type="password" label="新密码" autoComplete="new-password" minLength={8} required placeholder="至少 8 个字符" />
      <TextField id="confirm_password" name="confirm_password" type="password" label="确认新密码" autoComplete="new-password" minLength={8} required />
      {error && <p role="alert" className="rounded-[var(--radius)] border border-[#efc7c4] bg-[#fff5f4] px-3 py-2 text-sm text-[var(--danger)]">{error}</p>}
      <Button type="submit" variant="brand" size="lg" className="w-full" disabled={pending}>
        <KeyRound size={17} aria-hidden="true" />
        {pending ? "正在更新..." : "更新密码并继续"}
      </Button>
    </form>
  );
}
