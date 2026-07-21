export default function Loading() {
  return (
    <main className="mx-auto max-w-7xl animate-pulse px-5 py-12 lg:px-8" aria-busy="true" aria-label="正在加载">
      <div className="h-5 w-24 rounded bg-[var(--surface-muted)]" />
      <div className="mt-4 h-10 w-72 max-w-full rounded bg-[var(--surface-muted)]" />
      <div className="mt-9 grid gap-5 md:grid-cols-2 lg:grid-cols-3">{Array.from({ length: 6 }).map((_, index) => <div key={index} className="h-80 rounded-[var(--radius)] bg-[var(--surface-muted)]" />)}</div>
    </main>
  );
}
