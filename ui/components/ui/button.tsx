import type { ButtonHTMLAttributes } from "react";
import { clsx } from "clsx";

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "brand" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md" | "lg";
};

export function Button({ className, variant = "secondary", size = "md", ...props }: ButtonProps) {
  return (
    <button
      className={clsx(
        "inline-flex min-h-9 items-center justify-center gap-2 rounded-[var(--radius)] border px-3 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        variant === "brand" && "border-[var(--brand)] bg-[var(--brand)] text-white hover:bg-[var(--brand-hover)]",
        variant === "secondary" && "border-[var(--border)] bg-[var(--surface)] text-[var(--foreground)] hover:border-[var(--border-strong)] hover:bg-[var(--surface-subtle)]",
        variant === "ghost" && "border-transparent bg-transparent text-[var(--muted)] hover:bg-[var(--surface-muted)] hover:text-[var(--foreground)]",
        variant === "danger" && "border-[#efc7c4] bg-[#fff5f4] text-[var(--danger)] hover:bg-[#fde8e6]",
        size === "sm" && "min-h-8 px-2.5 text-xs",
        size === "lg" && "min-h-11 px-4",
        className,
      )}
      {...props}
    />
  );
}
