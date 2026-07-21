export function formatDate(value?: string | null) {
  if (!value) return "未发布";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(new Date(value));
}

export function formatRelativeDate(value?: string | null) {
  if (!value) return "未发布";
  const date = new Date(value);
  const days = Math.max(0, Math.floor((Date.now() - date.getTime()) / 86400000));
  if (days === 0) return "今天";
  if (days < 7) return `${days} 天前`;
  if (days < 30) return `${Math.floor(days / 7)} 周前`;
  if (days < 365) return `${Math.floor(days / 30)} 个月前`;
  return `${Math.floor(days / 365)} 年前`;
}

export function formatWordCount(value?: number | null) {
  return new Intl.NumberFormat("zh-CN").format(value ?? 0);
}

export function formatSource(value?: string | null) {
  if (value === "manual") return "原创";
  if (value === "import") return "导入";
  if (value === "agent") return "Agent";
  return value || "原创";
}
