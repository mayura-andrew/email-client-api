const number = new Intl.NumberFormat("en-US");

export const formatNumber = (value: number) => number.format(Math.round(value));

export function timeAgo(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "—";
  const seconds = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function formatDuration(seconds: number): string {
  if (!isFinite(seconds) || seconds <= 0) return "—";
  if (seconds < 60) return `${Math.ceil(seconds)}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${Math.round(seconds % 60)}s`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

export function timeUntil(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "—";
  return formatDuration((new Date(iso).getTime() - now) / 1000);
}

export function initials(name: string): string {
  return name.split(/\s+/).filter(Boolean).slice(0, 2).map(part => part[0]?.toUpperCase()).join("") || "?";
}

/** Replaces the template tokens exactly like the delivery worker does. */
export function personalize(template: string, name: string, email: string, number = ""): string {
  return template.replaceAll("{{student_name}}", name).replaceAll("{{email}}", email).replaceAll("{{certificate_number}}", number);
}
