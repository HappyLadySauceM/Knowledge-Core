import type { ReactNode } from "react";
import { clsx } from "clsx";

export function Badge({ children, tone = "neutral" }: { children: ReactNode; tone?: "neutral" | "brand" | "success" | "warning" | "danger" }) {
  return (
    <span
      className={clsx(
        "inline-flex max-w-full items-center rounded-md border px-2 py-0.5 text-xs leading-5",
        tone === "neutral" && "border-[var(--border)] bg-[var(--surface-subtle)] text-[var(--muted)]",
        tone === "brand" && "border-[#d8d5ff] bg-[var(--brand-soft)] text-[var(--brand)]",
        tone === "success" && "border-[#b8e0cd] bg-[#effaf4] text-[var(--success)]",
        tone === "warning" && "border-[#efd3ae] bg-[#fff8ed] text-[var(--warning)]",
        tone === "danger" && "border-[#efc7c4] bg-[#fff5f4] text-[var(--danger)]",
      )}
    >
      {children}
    </span>
  );
}
