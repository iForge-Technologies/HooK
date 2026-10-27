import { useCallback, useId, useMemo, useState } from "react";
import type { Beat, SeriesPoint, Timings } from "../lib/api";
import { fmtDateTime, fmtMs, fmtStamp } from "../lib/format";
import { useSize } from "../lib/hooks";
import { useTip } from "./overlay";
import { STATUS } from "./status";

/* ---------------- heartbeat bar (after Uptime Kuma) ---------------- */

const PILL = 6, GAP = 3;

/** The latest checks as pills, newest on the right. "Retrying" is hollow so it
 *  never relies on hue alone to differ from "slow". */
export function HeartbeatBar({ beats, height = 26, className = "" }: { beats: Beat[]; height?: number; className?: string }) {
  const [ref, { w }] = useSize();
  const tip = useTip();
  const slots = Math.max(1, Math.floor((w + GAP) / (PILL + GAP)));
  const shown = beats.slice(-slots);
  const empty = Math.max(0, slots - shown.length);
  return (
    <div ref={ref} className={`flex min-w-0 items-stretch justify-end gap-[3px] ${className}`} style={{ height }} role="img" aria-label={`Last ${shown.length} checks: ${shown.filter((b) => b.status === "down").length} down`}>
      {w > 0 &&
        Array.from({ length: empty }, (_, i) => <span key={"e" + i} className="w-[6px] shrink-0 rounded-full bg-surface-3/70" />)}
      {w > 0 &&
        shown.map((b) => {
          const s = STATUS[b.status];
          const hollow = b.status === "pending";
          return (
            <span
              key={b.ts}
              className="w-[6px] shrink-0 rounded-full transition-transform hover:scale-y-110"
              style={hollow ? { boxShadow: `inset 0 0 0 1.5px ${s.color}` } : { background: s.color }}
              onPointerEnter={(e) =>
                tip.show(e.clientX, e.clientY, {
                  title: fmtStamp(b.ts),
                  rows: [
                    { label: s.label, value: b.status === "down" ? "—" : fmtMs(b.latency), color: s.color, hollow },
                    ...(b.code ? [{ label: "HTTP", value: String(b.code) }] : []),
                  ],
                  note: b.msg && b.status !== "up" ? b.msg : undefined,
                })
              }
              onPointerLeave={tip.hide}
            />
          );
        })}
    </div>
  );
}

/* ---------------- response time line (after the analytics revenue line) ---------------- */

/** Monotone cubic (Fritsch–Carlson): smooth like the bento curves, but it never
 *  overshoots, so the line can't show a response time that didn't happen. */
export function smooth(P: [number, number][]) {
  const n = P.length;
  if (n < 3) return n ? "M" + P.map((p) => p.join(",")).join("L") : "";
  const dx: number[] = [], m: number[] = [], t: number[] = [];
  for (let i = 0; i < n - 1; i++) {
    dx[i] = P[i + 1][0] - P[i][0];
    m[i] = dx[i] ? (P[i + 1][1] - P[i][1]) / dx[i] : 0;
  }
  t[0] = m[0];
  t[n - 1] = m[n - 2];
  for (let i = 1; i < n - 1; i++) t[i] = m[i - 1] * m[i] <= 0 ? 0 : (m[i - 1] + m[i]) / 2;
  for (let i = 0; i < n - 1; i++) {
    if (m[i] === 0) {
      t[i] = t[i + 1] = 0;
      continue;
    }
    const a = t[i] / m[i], b = t[i + 1] / m[i], s = a * a + b * b;
    if (s > 9) {
      const k = 3 / Math.sqrt(s);
      t[i] = k * a * m[i];
      t[i + 1] = k * b * m[i];
    }
  }
  let d = `M${P[0][0]},${P[0][1]}`;
  for (let i = 0; i < n - 1; i++) {
    const h = dx[i] / 3;
    d += `C${P[i][0] + h},${P[i][1] + t[i] * h} ${P[i + 1][0] - h},${P[i + 1][1] - t[i + 1] * h} ${P[i + 1][0]},${P[i + 1][1]}`;
  }
  return d;
}

/** Only successful checks have a meaningful response time. */
const plotted = (s: string) => s === "up" || s === "degraded";

export function niceStep(span: number, target = 4) {
  const raw = span / target, mag = Math.pow(10, Math.floor(Math.log10(raw || 1)));
  return [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) || raw;
}

export function timeLabel(ts: number, spanMs: number) {
  const d = new Date(ts);
  if (spanMs <= 15 * 60e3) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  if (spanMs <= 36 * 3600e3) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (spanMs <= 8 * 86400e3) return d.toLocaleDateString([], { weekday: "short", day: "numeric" });
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

export function ResponseChart({ points, hourly, height = 240 }: { points: SeriesPoint[]; hourly: boolean; height?: number }) {
  const [ref, { w: W }] = useSize();
  const tip = useTip();
  const id = useId().replace(/:/g, "");
  const [hover, setHover] = useState<number | null>(null);
  const H = height, top = 16, bot = 26, left = 6, right = 52;
  // Hourly buckets carry an average of their successful checks even in a bad hour.
  const onLine = useCallback((p: SeriesPoint) => (hourly ? (p.up ?? 0) > 0 : plotted(p.status)), [hourly]);

  const geo = useMemo(() => {
    if (!W || points.length === 0) return null;
    const t0 = points[0].ts, t1 = points[points.length - 1].ts, span = Math.max(1, t1 - t0);
    const lat = points.filter(onLine).map((p) => p.latency);
    const hi = Math.max(10, ...lat) * 1.15;
    const step = niceStep(hi);
    const yMax = Math.ceil(hi / step) * step;
    const X = (t: number) => left + ((t - t0) / span) * (W - left - right);
    const Y = (v: number) => top + (1 - Math.min(v, yMax) / yMax) * (H - top - bot);
    // Break the line where checks stopped (paused) or the monitor was down.
    const deltas = points.slice(1).map((p, i) => p.ts - points[i].ts).sort((a, b) => a - b);
    const typical = deltas[Math.floor(deltas.length / 2)] || 60_000;
    const runs: [number, number][][] = [];
    let cur: [number, number][] = [];
    points.forEach((p, i) => {
      const gap = i > 0 && p.ts - points[i - 1].ts > typical * 3.5;
      if (!onLine(p) || gap) {
        if (cur.length) runs.push(cur);
        cur = [];
      }
      if (onLine(p)) cur.push([X(p.ts), Y(p.latency)]);
    });
    if (cur.length) runs.push(cur);
    const dense = points.length > 160;
    const line = runs.map((r) => (dense ? "M" + r.map((p) => p.join(",")).join("L") : smooth(r))).join("");
    const area = runs
      .filter((r) => r.length > 1)
      .map((r) => (dense ? "M" + r.map((p) => p.join(",")).join("L") : smooth(r)) + `L${r[r.length - 1][0]},${H - bot}L${r[0][0]},${H - bot}Z`)
      .join("");
    // Failure bands run from a failed point to the next point: red for down,
    // amber for an unconfirmed failure (retrying).
    const bands = points
      .map((p, i) => {
        const color = hourly ? (p.down ? "var(--down)" : null) : p.status === "down" ? "var(--down)" : p.status === "pending" ? "var(--warn)" : null;
        return color ? { x: X(p.ts), x2: X(points[i + 1]?.ts ?? p.ts + typical), color } : null;
      })
      .filter(Boolean) as { x: number; x2: number; color: string }[];
    const ticks: number[] = [];
    for (let v = 0; v <= yMax + 1e-9; v += step) ticks.push(v);
    const nx = Math.max(2, Math.min(6, Math.floor(W / 120)));
    const xt: number[] = Array.from({ length: nx }, (_, i) => t0 + (span * i) / (nx - 1));
    return { X, Y, line, area, bands, ticks, xt, span, t0 };
  }, [W, H, points, onLine, hourly]);

  const last = points.length ? points[points.length - 1] : null;

  const move = (e: React.PointerEvent<SVGRectElement>) => {
    if (!geo) return;
    const r = e.currentTarget.ownerSVGElement!.getBoundingClientRect();
    const x = e.clientX - r.left;
    // nearest point by x (points are time-sorted)
    let lo = 0, hi = points.length - 1;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (geo.X(points[mid].ts) < x) lo = mid;
      else hi = mid;
    }
    const i = Math.abs(geo.X(points[lo].ts) - x) < Math.abs(geo.X(points[hi].ts) - x) ? lo : hi;
    const p = points[i];
    setHover(i);
    const s = STATUS[p.status];
    tip.show(r.left + geo.X(p.ts), r.top + (onLine(p) ? geo.Y(p.latency) : H - bot), {
      title: hourly ? new Date(p.ts).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) + " (1h)" : fmtDateTime(p.ts),
      rows: [
        { label: hourly ? "Avg response" : "Response", value: onLine(p) ? fmtMs(p.latency) : "—", color: "var(--series)" },
        ...(hourly && p.max ? [{ label: "Slowest", value: fmtMs(p.max) }] : []),
        ...(hourly && p.down ? [{ label: "Failed checks", value: String(p.down), color: STATUS.down.color }] : []),
        ...(!hourly ? [{ label: "Status", value: s.label, color: s.color, hollow: p.status === "pending" }] : []),
      ],
      note: p.msg,
    });
  };

  return (
    <div ref={ref} className="relative w-full" style={{ height: H }}>
      {geo && W > 0 && (
        <svg width={W} height={H} className="block" role="img" aria-label={`Response time, ${points.length} points, latest ${last ? fmtMs(last.latency) : "none"}`}>
          <defs>
            <linearGradient id={`${id}a`} x1="0" x2="0" y1="0" y2="1">
              <stop offset="0" stopColor="var(--series)" stopOpacity=".22" />
              <stop offset="1" stopColor="var(--series)" stopOpacity="0" />
            </linearGradient>
            <linearGradient id={`${id}m`} x1="0" x2="1">
              <stop offset="0" stopColor="#fff" stopOpacity=".25" />
              <stop offset=".3" stopColor="#fff" stopOpacity="1" />
            </linearGradient>
            <mask id={`${id}k`}>
              <rect width={W} height={H} fill={`url(#${id}m)`} />
            </mask>
          </defs>
          {geo.ticks.map((v) => (
            <g key={v}>
              <line x1={left} x2={W - right + 8} y1={geo.Y(v)} y2={geo.Y(v)} stroke="var(--line)" />
              <text x={W - 4} y={geo.Y(v) + 3.5} textAnchor="end" fontSize={10.5} fill="var(--fg-3)" className="font-mono tnum">
                {v >= 1000 ? `${+(v / 1000).toFixed(1)}s` : `${v}ms`}
              </text>
            </g>
          ))}
          {geo.bands.map((b, i) => (
            <rect key={i} x={b.x} y={top} width={Math.max(2, b.x2 - b.x)} height={H - top - bot} fill={b.color} opacity={0.16} />
          ))}
          {geo.bands.map((b, i) => (
            <rect key={"m" + i} x={b.x} y={H - bot - 3} width={Math.max(2, b.x2 - b.x)} height={3} rx={1.5} fill={b.color} />
          ))}
          <path d={geo.area} fill={`url(#${id}a)`} mask={`url(#${id}k)`} />
          <path d={geo.line} fill="none" stroke="var(--series)" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" />
          {geo.xt.map((t, i) => (
            <text key={i} x={geo.X(t)} y={H - 7} fontSize={10.5} fill="var(--fg-3)" className="font-mono" textAnchor={i === 0 ? "start" : i === geo.xt.length - 1 ? "end" : "middle"}>
              {timeLabel(t, geo.span)}
            </text>
          ))}
          {last && onLine(last) && <circle cx={geo.X(last.ts)} cy={geo.Y(last.latency)} r={4.5} fill="var(--series)" stroke="var(--surface)" strokeWidth={2} />}
          {hover !== null && points[hover] && (
            <>
              <line x1={geo.X(points[hover].ts)} x2={geo.X(points[hover].ts)} y1={top} y2={H - bot} stroke="var(--fg-3)" strokeWidth={1} />
              {onLine(points[hover]) && <circle cx={geo.X(points[hover].ts)} cy={geo.Y(points[hover].latency)} r={4.5} fill="var(--series)" stroke="var(--surface)" strokeWidth={2} />}
            </>
          )}
          <rect width={W} height={H} fill="transparent" onPointerMove={move} onPointerLeave={() => { setHover(null); tip.hide(); }} />
        </svg>
      )}
    </div>
  );
}

/* ---------------- request waterfall ---------------- */

const PHASES: { key: keyof Timings; label: string; color: string; help: string }[] = [
  { key: "dns", label: "DNS", color: "var(--cat-1)", help: "Resolving the hostname" },
  { key: "connect", label: "Connect", color: "var(--cat-2)", help: "TCP handshake" },
  { key: "tls", label: "TLS", color: "var(--cat-3)", help: "TLS handshake" },
  { key: "ttfb", label: "Server wait", color: "var(--cat-4)", help: "Time to first byte after sending" },
  { key: "transfer", label: "Download", color: "var(--cat-5)", help: "Reading the response body" },
];

export function Waterfall({ timings }: { timings: Timings }) {
  const tip = useTip();
  const parts = PHASES.map((p) => ({ ...p, v: timings[p.key] }));
  const sum = Math.max(1, parts.reduce((a, p) => a + p.v, 0));
  return (
    <div>
      <div className="flex h-3.5 w-full gap-[2px]" role="img" aria-label={parts.map((p) => `${p.label} ${p.v} ms`).join(", ")}>
        {parts
          .filter((p) => p.v > 0)
          .map((p) => (
            <span
              key={p.key}
              className="h-full min-w-[3px] rounded-[4px]"
              style={{ flex: `${p.v} 1 0`, background: p.color }}
              onPointerEnter={(e) => tip.show(e.clientX, e.clientY, { title: p.help, rows: [{ label: p.label, value: fmtMs(p.v), color: p.color }] })}
              onPointerLeave={tip.hide}
            />
          ))}
      </div>
      <dl className="mt-4 grid grid-cols-2 gap-x-6 gap-y-2.5 sm:grid-cols-3 lg:grid-cols-6">
        {parts.map((p) => (
          <div key={p.key} className="min-w-0">
            <dt className="flex items-center gap-1.5 text-xs text-fg-2">
              <i className="size-2 shrink-0 rounded-[3px]" style={{ background: p.color }} />
              {p.label}
            </dt>
            <dd className="tnum mt-0.5 font-mono text-sm">
              {fmtMs(p.v)} <span className="text-xs text-fg-3">{Math.round((p.v / sum) * 100)}%</span>
            </dd>
          </div>
        ))}
        <div>
          <dt className="text-xs text-fg-2">Total</dt>
          <dd className="tnum mt-0.5 font-mono text-sm font-semibold">{fmtMs(timings.total)}</dd>
        </div>
      </dl>
    </div>
  );
}
