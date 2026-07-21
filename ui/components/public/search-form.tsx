import { Search } from "lucide-react";

export function SearchForm({ defaultValue = "", action = "/knowledge" }: { defaultValue?: string; action?: string }) {
  return (
    <form action={action} className="flex min-w-0 items-center gap-2 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--surface)] px-3 py-2 shadow-sm">
      <Search size={17} className="shrink-0 text-[var(--faint)]" aria-hidden="true" />
      <label className="sr-only" htmlFor="site-search">搜索文章</label>
      <input id="site-search" name="q" defaultValue={defaultValue} placeholder="搜索标题、摘要或标签" className="min-w-0 flex-1 bg-transparent text-sm text-[var(--foreground)] outline-none placeholder:text-[var(--faint)]" />
      <button type="submit" className="rounded bg-[var(--brand)] px-3 py-1.5 text-xs font-medium text-white hover:bg-[var(--brand-hover)]">搜索</button>
    </form>
  );
}
