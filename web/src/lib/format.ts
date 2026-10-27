/** Uptime as a percentage, floored so 99.996% never shows as 100%. */
export function fmtUptime(u: number | null | undefined): string {
  if (u == null) return "—";
  if (u >= 1) return "100%";
  return (Math.floor(u * 10000) / 100).toFixed(2) + "%";
}

export function fmtMs(ms: number | null | undefined): string {
  if (ms == null) return "—";
  if (ms < 1) return "<1 ms";
  if (ms >= 10000) return (ms / 1000).toFixed(1) + " s";
  return Math.round(ms).toLocaleString("en-US") + " ms";
}

export function fmtDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${sec}s`;
  return `${sec}s`;
}

export function fmtAgo(ts: number, now = Date.now()): string {
  if (!ts) return "never";
  const s = Math.round((now - ts) / 1000);
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  return fmtDuration(now - ts).split(" ")[0] + " ago";
}

export function fmtTime(ts: number): string {
  return new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function fmtDateTime(ts: number): string {
  return new Date(ts).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

/** Date with seconds, for individual checks. */
export function fmtStamp(ts: number): string {
  return new Date(ts).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function fmtInterval(s: number): string {
  if (s % 3600 === 0) return `${s / 3600}h`;
  if (s % 60 === 0) return `${s / 60}m`;
  return `${s}s`;
}

export function daysUntil(ts: number): number {
  return Math.floor((ts - Date.now()) / 86400000);
}

export const TYPE_LABEL: Record<string, string> = {
  http: "HTTP",
  keyword: "Keyword",
  tcp: "TCP port",
  dns: "DNS",
  tls: "TLS cert",
  postgres: "PostgreSQL",
  mysql: "MySQL",
  redis: "Redis",
  mongodb: "MongoDB",
  push: "Push",
  vercel: "Vercel deploys",
  render: "Render deploys",
};

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

/** Bytes in binary units (1 KB = 1024 B), as most server tools show them. */
export function fmtBytes(b: number | null | undefined, digits = 1): string {
  if (b == null || !isFinite(b)) return "—";
  let i = 0;
  while (Math.abs(b) >= 1024 && i < UNITS.length - 1) {
    b /= 1024;
    i++;
  }
  return `${i === 0 ? Math.round(b) : b.toFixed(b >= 100 ? 0 : digits)} ${UNITS[i]}`;
}

export const fmtRate = (bps: number | null | undefined) => (bps == null ? "—" : fmtBytes(bps) + "/s");

export function fmtPct(v: number | null | undefined, digits = 0): string {
  return v == null || !isFinite(v) ? "—" : `${v.toFixed(digits)}%`;
}

/** Uptime from seconds: "12d 4h", "3h 20m". */
export const fmtUptimeSecs = (s: number) => fmtDuration(s * 1000);

export const memPct = (m: { memUsed: number; memTotal: number }) => (m.memTotal > 0 ? (m.memUsed / m.memTotal) * 100 : 0);
