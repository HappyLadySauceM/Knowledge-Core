import type { InputHTMLAttributes, TextareaHTMLAttributes } from "react";
import { clsx } from "clsx";

type BaseProps = { label?: string; error?: string; hint?: string; className?: string };

export function TextField({ label, error, hint, className, id, ...props }: BaseProps & InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className={clsx("flex flex-col gap-1.5 text-sm", className)} htmlFor={id}>
      {label && <span className="font-medium text-[var(--foreground)]">{label}</span>}
      <input
        id={id}
        className={clsx(
          "min-h-10 rounded-[var(--radius)] border bg-[var(--surface)] px-3 text-sm text-[var(--foreground)] placeholder:text-[var(--faint)]",
          "focus:border-[var(--brand)] focus:ring-2 focus:ring-[#dedcff]",
          error ? "border-[#dd8c86]" : "border-[var(--border)]",
        )}
        {...props}
      />
      {error ? <span className="text-xs text-[var(--danger)]">{error}</span> : hint ? <span className="text-xs text-[var(--faint)]">{hint}</span> : null}
    </label>
  );
}

export function TextArea({ label, error, hint, className, id, ...props }: BaseProps & TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <label className={clsx("flex flex-col gap-1.5 text-sm", className)} htmlFor={id}>
      {label && <span className="font-medium text-[var(--foreground)]">{label}</span>}
      <textarea
        id={id}
        className={clsx(
          "min-h-28 resize-y rounded-[var(--radius)] border bg-[var(--surface)] px-3 py-2 text-sm text-[var(--foreground)] placeholder:text-[var(--faint)]",
          "focus:border-[var(--brand)] focus:ring-2 focus:ring-[#dedcff]",
          error ? "border-[#dd8c86]" : "border-[var(--border)]",
        )}
        {...props}
      />
      {error ? <span className="text-xs text-[var(--danger)]">{error}</span> : hint ? <span className="text-xs text-[var(--faint)]">{hint}</span> : null}
    </label>
  );
}
