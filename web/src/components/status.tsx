import type { Status } from "../lib/api";

export const STATUS: Record<Status, { label: string; color: string; cls: string }> = {
  up: { label: "Up", color: "var(--up)", cls: "text-up" },
  degraded: { label: "Slow", color: "var(--warn)", cls: "text-warn" },
  pending: { label: "Retrying", color: "var(--warn)", cls: "text-warn" },
  down: { label: "Down", color: "var(--down)", cls: "text-down" },
  paused: { label: "Paused", color: "var(--paused)", cls: "text-paused" },
  maintenance: { label: "Maintenance", color: "var(--maint)", cls: "text-maint" },
};

/** A shape per status so meaning never rests on colour alone. */
export function StatusIcon({ status, className = "size-3.5" }: { status: Status; className?: string }) {
  const c = STATUS[status]?.color ?? "var(--paused)";
  return (
    <svg viewBox="0 0 16 16" className={`shrink-0 ${className}`} aria-hidden>
      {status === "up" && (
        <>
          <circle cx="8" cy="8" r="7" fill={c} />
          <path d="M4.8 8.2l2.1 2.1 4.3-4.6" fill="none" stroke="#000" strokeOpacity=".75" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </>
      )}
      {status === "down" && (
        <>
          <circle cx="8" cy="8" r="7" fill={c} />
          <path d="M5.5 5.5l5 5M10.5 5.5l-5 5" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" />
        </>
      )}
      {status === "degraded" && (
        <>
          <path d="M8 1.5l6.8 12a1 1 0 0 1-.9 1.5H2.1a1 1 0 0 1-.9-1.5z" fill={c} />
          <path d="M8 6v3.6" stroke="#000" strokeOpacity=".75" strokeWidth="1.7" strokeLinecap="round" />
          <circle cx="8" cy="12" r="1" fill="#000" fillOpacity=".75" />
        </>
      )}
      {status === "pending" && (
        <>
          <circle cx="8" cy="8" r="6.1" fill="none" stroke={c} strokeWidth="1.8" />
          <path d="M8 4.8V8l2.2 1.4" fill="none" stroke={c} strokeWidth="1.7" strokeLinecap="round" />
        </>
      )}
      {status === "maintenance" && (
        <>
          <circle cx="8" cy="8" r="7" fill={c} />
          <path d="M10.6 4.6a2.4 2.4 0 0 0-3.2 3l-3 3a.9.9 0 0 0 1.3 1.3l3-3a2.4 2.4 0 0 0 3-3.2l-1.3 1.3-1.1-.2-.2-1.1z" fill="#fff" />
        </>
      )}
      {status === "paused" && (
        <>
          <circle cx="8" cy="8" r="7" fill={c} />
          <path d="M6.3 5.2v5.6M9.7 5.2v5.6" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" />
        </>
      )}
    </svg>
  );
}

/** A monitor that hasn't been checked yet is "pending" but not actually retrying. */
export function shownStatus(m: { active: boolean; status: Status; lastCheck: number }): Status | "new" {
  if (!m.active) return "paused";
  return m.lastCheck === 0 ? "new" : m.status;
}

export function StatusBadge({ status: st, size = "sm", pulse, label }: { status: Status | "new"; size?: "sm" | "lg"; pulse?: boolean; label?: string }) {
  const status: Status = st === "new" ? "pending" : st;
  const base = st === "new" ? { ...STATUS.pending, label: "Waiting" } : (STATUS[status] ?? STATUS.paused);
  const s = label ? { ...base, label } : base;
  const big = size === "lg";
  return (
    <span className={`inline-flex items-center font-semibold ${big ? "h-8 gap-2 rounded-xl px-3 text-sm" : "h-6 gap-1.5 rounded-lg px-2 text-xs"} ${s.cls}`} style={{ background: `color-mix(in oklab, ${s.color} 14%, transparent)` }}>
      <span className={`rounded-full ${pulse && status === "down" ? "pulse-dot" : ""}`}>
        <StatusIcon status={status} className={big ? "size-4" : "size-3.5"} />
      </span>
      <span className="text-fg">{s.label}</span>
    </span>
  );
}

/** Server status in monitor terms, for badges and icons. */
export function serverBadge(status: "new" | "online" | "offline"): { status: Status | "new"; label: string } {
  if (status === "online") return { status: "up", label: "Online" };
  if (status === "offline") return { status: "down", label: "Offline" };
  return { status: "new", label: "Waiting" };
}
