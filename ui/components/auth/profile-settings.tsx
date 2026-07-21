"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { LogOut, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TextArea, TextField } from "@/components/ui/text-field";
import type { ApiEnvelope, User } from "@/lib/api/types";

export function ProfileSettings() {
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");

  useEffect(() => {
    fetch("/api/session/me", { cache: "no-store" }).then(async (response) => {
      if (response.status === 401) {
        router.replace("/login?next=/profile");
        return;
      }
      const payload = await response.json() as ApiEnvelope<User>;
      if (response.ok) setUser(payload.data);
    }).finally(() => setLoading(false));
  }, [router]);

  async function updateProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setMessage("");
    const form = new FormData(event.currentTarget);
    const response = await fetch("/api/session/me", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: form.get("username"), email: form.get("email"), avatar: form.get("avatar"), bio: form.get("bio") }),
    });
    const payload = await response.json() as ApiEnvelope<User>;
    if (response.ok) {
      setUser(payload.data);
      setMessage("个人资料已保存");
    } else setMessage(payload.message || "保存失败");
  }

  async function changePassword(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setMessage("");
    const form = new FormData(event.currentTarget);
    const response = await fetch("/api/session/password", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ old_password: form.get("old_password"), new_password: form.get("new_password") }),
    });
    const payload = await response.json() as ApiEnvelope<unknown>;
    setMessage(response.ok ? "密码已更新" : payload.message || "密码更新失败");
    if (response.ok) event.currentTarget.reset();
  }

  async function logout() {
    await fetch("/api/session/logout", { method: "POST" });
    router.replace("/");
    router.refresh();
  }

  if (loading) return <div className="h-64 animate-pulse rounded-[var(--radius)] bg-[var(--surface-muted)]" aria-label="正在加载个人资料" />;
  if (!user) return <p className="text-sm text-[var(--muted)]">无法加载个人资料。</p>;

  return (
    <div className="grid gap-10 lg:grid-cols-[minmax(0,1fr)_340px]">
      <form onSubmit={updateProfile} className="space-y-5">
        <div><h2 className="text-lg font-semibold">基本信息</h2><p className="mt-1 text-sm text-[var(--muted)]">这些信息用于账号识别和公开作者资料。</p></div>
        <div className="grid gap-5 sm:grid-cols-2"><TextField id="username" name="username" label="用户名" defaultValue={user.username} required /><TextField id="email" name="email" type="email" label="邮箱" defaultValue={user.email} /></div>
        <TextField id="avatar" name="avatar" type="url" label="头像地址" defaultValue={user.avatar} placeholder="https://..." />
        <TextArea id="bio" name="bio" label="个人简介" defaultValue={user.bio} maxLength={400} />
        <Button type="submit" variant="brand"><Save size={16} aria-hidden="true" /> 保存资料</Button>
      </form>

      <div className="space-y-8 border-t border-[var(--border)] pt-8 lg:border-l lg:border-t-0 lg:pl-8 lg:pt-0">
        <form onSubmit={changePassword} className="space-y-4"><div><h2 className="text-lg font-semibold">修改密码</h2><p className="mt-1 text-sm text-[var(--muted)]">更新后请使用新密码登录。</p></div><TextField id="old_password" name="old_password" type="password" label="当前密码" autoComplete="current-password" required /><TextField id="new_password" name="new_password" type="password" label="新密码" autoComplete="new-password" minLength={8} required /><Button type="submit">更新密码</Button></form>
        <div className="border-t border-[var(--border)] pt-6"><Button type="button" variant="ghost" onClick={logout}><LogOut size={16} aria-hidden="true" /> 退出登录</Button></div>
      </div>
      {message && <p role="status" className="lg:col-span-2 rounded-[var(--radius)] bg-[var(--surface-subtle)] px-3 py-2 text-sm text-[var(--muted)]">{message}</p>}
    </div>
  );
}
