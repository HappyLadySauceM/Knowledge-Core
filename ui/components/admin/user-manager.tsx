"use client";

import { useCallback, useEffect, useState } from "react";
import { KeyRound, Search, ShieldCheck, UserRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { browserApi } from "@/lib/api/browser";
import type { ListResponse, User } from "@/lib/api/types";
import { formatDate } from "@/lib/format";

export function UserManager() {
  const [users, setUsers] = useState<User[]>([]);
  const [keyword, setKeyword] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const load = useCallback(async () => { setLoading(true); try { const params = new URLSearchParams({ page: "1", page_size: "50" }); if (keyword.trim()) params.set("keyword", keyword.trim()); const data = await browserApi<ListResponse<User>>(`/api/backend/admin/users?${params}`); setUsers(data.items); } catch (reason) { setError(reason instanceof Error ? reason.message : "加载失败"); } finally { setLoading(false); } }, [keyword]);
  useEffect(() => {
    let cancelled = false;
    const params = new URLSearchParams({ page: "1", page_size: "50" });
    if (keyword.trim()) params.set("keyword", keyword.trim());
    browserApi<ListResponse<User>>(`/api/backend/admin/users?${params}`)
      .then((data) => { if (!cancelled) setUsers(data.items); })
      .catch((reason) => { if (!cancelled) setError(reason instanceof Error ? reason.message : "加载失败"); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [keyword]);
  async function update(user: User, input: Partial<User>) { try { await browserApi(`/api/backend/admin/users/${user.id}`, { method: "PATCH", body: JSON.stringify(input) }); await load(); } catch (reason) { setError(reason instanceof Error ? reason.message : "更新失败"); } }
  async function resetPassword(user: User) { const password = window.prompt(`为 ${user.username} 设置新密码`); if (!password) return; try { await browserApi(`/api/backend/admin/users/${user.id}/password`, { method: "PUT", body: JSON.stringify({ password }) }); } catch (reason) { setError(reason instanceof Error ? reason.message : "重置失败"); } }
  return (
    <div className="mx-auto max-w-7xl px-5 py-8 lg:px-8"><header><p className="text-sm text-[var(--muted)]">访问控制</p><h1 className="mt-1 text-2xl font-semibold">用户</h1></header><form className="mt-7 flex max-w-lg items-center gap-2 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] px-3" onSubmit={(event) => { event.preventDefault(); void load(); }}><Search size={16} className="text-[var(--faint)]" aria-hidden="true" /><label htmlFor="user-search" className="sr-only">搜索用户</label><input id="user-search" value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder="搜索用户名或邮箱" className="min-h-10 flex-1 bg-transparent text-sm outline-none" /></form>{error && <p role="alert" className="mt-5 rounded border border-[#efc7c4] bg-[#fff5f4] px-3 py-2 text-sm text-[var(--danger)]">{error}</p>}<div className="mt-5 overflow-x-auto rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)]"><table className="w-full min-w-[850px] border-collapse text-left text-sm"><thead className="bg-[var(--surface-subtle)] text-xs text-[var(--muted)]"><tr><th className="px-4 py-3 font-medium">用户</th><th className="px-4 py-3 font-medium">角色</th><th className="px-4 py-3 font-medium">状态</th><th className="px-4 py-3 font-medium">创建时间</th><th className="px-4 py-3 text-right font-medium">操作</th></tr></thead><tbody className="divide-y divide-[var(--border)]">{users.map((user) => <tr key={user.id}><td className="px-4 py-3"><div className="flex items-center gap-3"><span className="grid size-8 place-items-center rounded-full bg-[var(--surface-muted)] text-[var(--muted)]">{user.role === "admin" ? <ShieldCheck size={16} /> : <UserRound size={16} />}</span><div><p className="font-medium">{user.username}</p><p className="mt-1 text-xs text-[var(--faint)]">{user.email || "未设置邮箱"}</p></div></div></td><td className="px-4 py-3"><select value={user.role} onChange={(event) => update(user, { role: event.target.value as User["role"] })} className="rounded border border-[var(--border)] bg-[var(--surface)] px-2 py-1.5 text-xs"><option value="user">用户</option><option value="admin">管理员</option></select></td><td className="px-4 py-3"><button type="button" onClick={() => update(user, { status: user.status === "active" ? "disabled" : "active" })} className={`rounded px-2 py-1 text-xs ${user.status === "active" ? "bg-[#effaf4] text-[var(--success)]" : "bg-[#fff5f4] text-[var(--danger)]"}`}>{user.status === "active" ? "正常" : "已禁用"}</button></td><td className="px-4 py-3 text-[var(--muted)]">{formatDate(user.created_at)}</td><td className="px-4 py-3"><div className="flex justify-end"><Button type="button" variant="ghost" size="sm" onClick={() => resetPassword(user)}><KeyRound size={15} aria-hidden="true" /> 重置密码</Button></div></td></tr>)}</tbody></table>{loading && <p className="px-4 py-10 text-center text-sm text-[var(--muted)]">正在加载...</p>}{!loading && !users.length && <p className="px-4 py-10 text-center text-sm text-[var(--muted)]">没有用户。</p>}</div></div>
  );
}
