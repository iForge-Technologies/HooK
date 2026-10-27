import { useId, useMemo, useState } from "react";
import type { MetricPoint } from "../lib/api";
import { fmtDateTime } from "../lib/format";
import { useSize } from "../lib/hooks";
import { niceStep, smooth, timeLabel } from "./charts";
import { useTip, type TipRow } from "./overlay";

/* ---------------- tick gauge (after the finance "runway" ticks) ---------------- */

/** A bar of ticks filled to `value` (0–100). It turns amber within 10 points
 *  of the limit and red at it; the number is always shown beside it. */
export function TickGauge({ label, value, limit, display, sub, ticks = 30 }: { label: string; value: number | null; limit?: number; display: string; sub?: string; ticks?: number }) {
  const v = value ?? 0;
  const color = limit && limit > 0 && v >= limit ? "var(--down)" : limit && limit > 0 && v >= limit - 10 ? "var(--warn)" : "var(--acc)";
  return (
    <div className="min-w-0">
      {(label || display) && (
        <div className="mb-1.5 flex items-baseline justify-between gap-2">
          <span className="text-xs font-medium text-fg-2">{label}</span>
          <span className="tnum font-mono text-sm">{display}</span>
        </div>
      )}
      <div className="flex h-3.5 items-end justify-between" role="meter" aria-label={label || "usage"} aria-valuenow={Math.round(v)} aria-valuemin={0} aria-valuemax={100}>
        {Array.from({ length: ticks }, (_, i) => {
          const on = value != null && (i + 0.5) / ticks <= v / 100;
          return <span key={i} className={`w-[3px] rounded-full transition-colors duration-300 ${i % 5 === 0 ? "h-full" : "h-2.5"}`} style={{ background: on ? color : "var(--surface-3)" }} />;
        })}
      </div>
      {sub && <div className="mt-1 truncate text-[11px] text-fg-3">{sub}</div>}
    </div>
  );
}

export function Sparkline({ values, height = 30, max }: { values: number[]; height?: number; max?: number }) {
  const [ref, { w }] = useSize();
  const id = useId().replace(/:/g, "");
  const d = useMemo(() => {
    if (!w || values.length < 2) return null;
    const hi = Math.max(max ?? 0, ...values, 1);
    const P = values.map((v, i) => [(i / (values.length - 1)) * w, height - 2 - (v / hi) * (height - 4)] as [number, number]);
    const line = smooth(P);
    return { line, area: `${line}L${w},${height}L0,${height}Z`, last: P[P.length - 1] };
  }, [w, values, height, max]);
  return (
    <div ref={ref} style={{ height }} aria-hidden>
      {d && (
        <svg width={w} height={height} className="block overflow-visible">
          <defs>
            <linearGradient id={id} x1="0" x2="0" y1="0" y2="1">
              <stop offset="0" stopColor="var(--series)" stopOpacity=".25" />
              <stop offset="1" stopColor="var(--series)" stopOpacity="0" />
            </linearGradient>
          </defs>
          <path d={d.area} fill={`url(#${id})`} />
          <path d={d.line} fill="none" stroke="var(--series)" strokeWidth={1.5} strokeLinecap="round" />
          <circle cx={d.last[0]} cy={d.last[1]} r={2.5} fill="var(--series)" />
        </svg>
      )}
    </div>
  );
}

/* ---------------- multi-series time chart ---------------- */

export type SeriesDef = {
  label: string;
  color: string;
  get: (p: MetricPoint) => number;
  peak?: (p: MetricPoint) => number | undefined; // rollup max, shown in the tooltip
};

type Geo = { X: (t: number) => number; Y: (v: number) => number; paths: string[]; area: string; ticks: number[]; xt: number[]; span: number; yMax: number };

function gapsBreak(points: MetricPoint[]) {
  const deltas = points.slice(1).map((p, i) => p.ts - points[i].ts).sort((a, b) => a - b);
  const typical = deltas[Math.floor(deltas.length / 2)] || 5000;
  return (i: number) => i > 0 && points[i].ts - points[i - 1].ts > typical * 3.5;
}

function path(P: [number, number][], dense: boolean) {
  return dense ? "M" + P.map((p) => p.join(",")).join("L") : smooth(P);
}

/** One y-axis, one unit. A dashed reference line marks a limit or capacity. */
export function TimeChart({
  points,
  series,
  fmt,
  yMax: fixedMax,
  reference,
  height = 200,
}: {
  points: MetricPoint[];
  series: SeriesDef[];
  fmt: (v: number) => string;
  yMax?: number;
  reference?: { value: number; label: string };
  height?: number;
}) {
  const [ref, { w: W }] = useSize();
  const tip = useTip();
  const id = useId().replace(/:/g, "");
  const [hover, setHover] = useState<number | null>(null);
  const H = height, top = 12, bot = 24, left = 4, right = 58;

  const geo = useMemo<Geo | null>(() => {
    if (!W || points.length < 2) return null;
    const t0 = points[0].ts, span = Math.max(1, points[points.length - 1].ts - t0);
    let hi = fixedMax ?? 0;
    if (fixedMax == null) {
      for (const p of points) for (const s of series) hi = Math.max(hi, s.get(p));
      if (reference) hi = Math.max(hi, reference.value);
      hi = Math.max(hi * 1.12, 1e-9);
    }
    const step = niceStep(hi, 4);
    const yMax = fixedMax ?? Math.ceil(hi / step) * step;
    const X = (t: number) => left + ((t - t0) / span) * (W - left - right);
    const Y = (v: number) => top + (1 - Math.min(Math.max(v, 0), yMax) / yMax) * (H - top - bot);
    const isGap = gapsBreak(points);
    const dense = points.length > 200;
    const paths = series.map((s) => {
      const runs: [number, number][][] = [];
      points.forEach((p, i) => {
        if (i === 0 || isGap(i)) runs.push([]);
        runs[runs.length - 1].push([X(p.ts), Y(s.get(p))]);
      });
      return runs.filter((r) => r.length > 1).map((r) => path(r, dense)).join("");
    });
    // area under the first (primary) series only
    const first: [number, number][][] = [];
    points.forEach((p, i) => {
      if (i === 0 || isGap(i)) first.push([]);
      first[first.length - 1].push([X(p.ts), Y(series[0].get(p))]);
    });
    const area = first
      .filter((r) => r.length > 1)
      .map((r) => path(r, dense) + `L${r[r.length - 1][0]},${H - bot}L${r[0][0]},${H - bot}Z`)
      .join("");
    const ticks: number[] = [];
    for (let v = 0; v <= yMax + 1e-9; v += fixedMax ? fixedMax / 4 : step) ticks.push(v);
    const nx = Math.max(2, Math.min(6, Math.floor(W / 120)));
    const xt = Array.from({ length: nx }, (_, i) => t0 + (span * i) / (nx - 1));
    return { X, Y, paths, area, ticks, xt, span, yMax };
  }, [W, H, points, series, fixedMax, reference]);

  const move = (e: React.PointerEvent<SVGRectElement>) => {
    if (!geo) return;
    const r = e.currentTarget.ownerSVGElement!.getBoundingClientRect();
    const x = e.clientX - r.left;
    let lo = 0, hi = points.length - 1;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (geo.X(points[mid].ts) < x) lo = mid;
      else hi = mid;
    }
    const i = Math.abs(geo.X(points[lo].ts) - x) < Math.abs(geo.X(points[hi].ts) - x) ? lo : hi;
    const p = points[i];
    setHover(i);
    const rows: TipRow[] = [];
    for (const s of series) {
      rows.push({ label: s.label, value: fmt(s.get(p)), color: s.color });
      const pk = s.peak?.(p);
      if (pk != null) rows.push({ label: `${s.label} peak`, value: fmt(pk) });
    }
    tip.show(r.left + geo.X(p.ts), r.top + geo.Y(series[0].get(p)), { title: fmtDateTime(p.ts), rows });
  };

  const last = points[points.length - 1];
  return (
    <div>
      {series.length > 1 && (
        <div className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-fg-2">
          {series.map((s) => (
            <span key={s.label} className="inline-flex items-center gap-1.5">
              <i className="h-0.5 w-3 rounded-full" style={{ background: s.color }} />
              {s.label}
              {last && <b className="tnum font-mono font-medium text-fg">{fmt(s.get(last))}</b>}
            </span>
          ))}
        </div>
      )}
      <div ref={ref} className="relative w-full" style={{ height: H }}>
        {geo && (
          <svg width={W} height={H} className="block" role="img" aria-label={series.map((s) => `${s.label} ${last ? fmt(s.get(last)) : ""}`).join(", ")}>
            <defs>
              <linearGradient id={`${id}a`} x1="0" x2="0" y1="0" y2="1">
                <stop offset="0" stopColor={series[0].color} stopOpacity=".2" />
                <stop offset="1" stopColor={series[0].color} stopOpacity="0" />
              </linearGradient>
            </defs>
            {geo.ticks.map((v) => (
              <g key={v}>
                <line x1={left} x2={W - right + 6} y1={geo.Y(v)} y2={geo.Y(v)} stroke="var(--line)" />
                <text x={W - 2} y={geo.Y(v) + 3.5} textAnchor="end" fontSize={10.5} fill="var(--fg-3)" className="tnum font-mono">
                  {fmt(v)}
                </text>
              </g>
            ))}
            {reference && reference.value <= geo.yMax && (
              <g>
                <line x1={left} x2={W - right + 6} y1={geo.Y(reference.value)} y2={geo.Y(reference.value)} stroke="var(--fg-3)" strokeDasharray="4 4" />
                <text x={left + 4} y={geo.Y(reference.value) - 5} fontSize={10.5} fill="var(--fg-2)">
                  {reference.label}
                </text>
              </g>
            )}
            <path d={geo.area} fill={`url(#${id}a)`} />
            {geo.paths.map((d, i) => (
              <path key={i} d={d} fill="none" stroke={series[i].color} strokeWidth={i === 0 ? 2 : 1.6} strokeLinecap="round" strokeLinejoin="round" />
            ))}
            {geo.xt.map((t, i) => (
              <text key={i} x={geo.X(t)} y={H - 6} fontSize={10.5} fill="var(--fg-3)" className="font-mono" textAnchor={i === 0 ? "start" : i === geo.xt.length - 1 ? "end" : "middle"}>
                {timeLabel(t, geo.span)}
              </text>
            ))}
            {hover !== null && points[hover] && (
              <>
                <line x1={geo.X(points[hover].ts)} x2={geo.X(points[hover].ts)} y1={top} y2={H - bot} stroke="var(--fg-3)" />
                {series.map((s, i) => (
                  <circle key={i} cx={geo.X(points[hover].ts)} cy={geo.Y(s.get(points[hover]))} r={3.5} fill={s.color} stroke="var(--surface)" strokeWidth={2} />
                ))}
              </>
            )}
            <rect width={W} height={H} fill="transparent" onPointerMove={move} onPointerLeave={() => { setHover(null); tip.hide(); }} />
          </svg>
        )}
      </div>
    </div>
  );
}

/* ---------------- mirrored waves (after the finance cash-flow card) ---------------- */

/** In above the centre line, out below, on one shared scale. */
export function MirrorChart({ points, up, down, fmt, height = 200 }: { points: MetricPoint[]; up: SeriesDef; down: SeriesDef; fmt: (v: number) => string; height?: number }) {
  const [ref, { w: W }] = useSize();
  const tip = useTip();
  const id = useId().replace(/:/g, "");
  const [hover, setHover] = useState<number | null>(null);
  const H = height, pad = 8, axis = 20, left = 4, right = 64, mid = (H - axis) / 2, amp = mid - pad;

  const geo = useMemo(() => {
    if (!W || points.length < 2) return null;
    const t0 = points[0].ts, span = Math.max(1, points[points.length - 1].ts - t0);
    let hi = 1;
    for (const p of points) hi = Math.max(hi, up.get(p), down.get(p));
    hi *= 1.1;
    const X = (t: number) => left + ((t - t0) / span) * (W - left - right);
    const isGap = gapsBreak(points);
    const dense = points.length > 200;
    const shape = (s: SeriesDef, dir: number) => {
      const runs: [number, number][][] = [];
      points.forEach((p, i) => {
        if (i === 0 || isGap(i)) runs.push([]);
        runs[runs.length - 1].push([X(p.ts), mid + dir * (s.get(p) / hi) * amp]);
      });
      const ok = runs.filter((r) => r.length > 1);
      return {
        line: ok.map((r) => path(r, dense)).join(""),
        area: ok.map((r) => path(r, dense) + `L${r[r.length - 1][0]},${mid}L${r[0][0]},${mid}Z`).join(""),
      };
    };
    const nx = Math.max(2, Math.min(5, Math.floor(W / 140)));
    const xt: number[] = Array.from({ length: nx }, (_, i) => t0 + (span * i) / (nx - 1));
    return { X, hi, u: shape(up, -1), d: shape(down, 1), span, xt };
  }, [W, H, points, up, down, amp, mid]);

  const move = (e: React.PointerEvent<SVGRectElement>) => {
    if (!geo) return;
    const r = e.currentTarget.ownerSVGElement!.getBoundingClientRect();
    const x = e.clientX - r.left;
    let lo = 0, hi = points.length - 1;
    while (hi - lo > 1) {
      const m = (lo + hi) >> 1;
      if (geo.X(points[m].ts) < x) lo = m;
      else hi = m;
    }
    const i = Math.abs(geo.X(points[lo].ts) - x) < Math.abs(geo.X(points[hi].ts) - x) ? lo : hi;
    const p = points[i];
    setHover(i);
    const rows: TipRow[] = [
      { label: up.label, value: fmt(up.get(p)), color: up.color },
      { label: down.label, value: fmt(down.get(p)), color: down.color },
    ];
    const pu = up.peak?.(p), pd = down.peak?.(p);
    if (pu != null) rows.splice(1, 0, { label: `${up.label} peak`, value: fmt(pu) });
    if (pd != null) rows.push({ label: `${down.label} peak`, value: fmt(pd) });
    tip.show(r.left + geo.X(p.ts), r.top + mid, { title: fmtDateTime(p.ts), rows });
  };

  const last = points[points.length - 1];
  return (
    <div>
      <div className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-fg-2">
        {[up, down].map((s, k) => (
          <span key={s.label} className="inline-flex items-center gap-1.5">
            <i className="h-0.5 w-3 rounded-full" style={{ background: s.color }} />
            {s.label} {k === 0 ? "↑ above" : "↓ below"}
            {last && <b className="tnum font-mono font-medium text-fg">{fmt(s.get(last))}</b>}
          </span>
        ))}
      </div>
      <div ref={ref} className="relative w-full" style={{ height: H }}>
        {geo && (
          <svg width={W} height={H} className="block" role="img" aria-label={`${up.label} and ${down.label}`}>
            <defs>
              {[up, down].map((s, k) => (
                <linearGradient key={k} id={`${id}${k}`} x1="0" x2="0" y1={k ? 0 : 1} y2={k ? 1 : 0}>
                  <stop offset="0" stopColor={s.color} stopOpacity="0.05" />
                  <stop offset="1" stopColor={s.color} stopOpacity="0.3" />
                </linearGradient>
              ))}
            </defs>
            {[-1, -0.5, 0.5, 1].map((k) => (
              <line key={k} x1={left} x2={W - right + 6} y1={mid + k * amp} y2={mid + k * amp} stroke="var(--line)" />
            ))}
            <line x1={left} x2={W - right + 6} y1={mid} y2={mid} stroke="var(--line-strong)" />
            {[-1, 1].map((k) => (
              <text key={k} x={W - 2} y={mid + k * amp + 3.5} textAnchor="end" fontSize={10.5} fill="var(--fg-3)" className="tnum font-mono">
                {fmt(geo.hi)}
              </text>
            ))}
            <text x={W - 2} y={mid + 3.5} textAnchor="end" fontSize={10.5} fill="var(--fg-3)" className="font-mono">
              0
            </text>
            <path d={geo.u.area} fill={`url(#${id}0)`} />
            <path d={geo.d.area} fill={`url(#${id}1)`} />
            <path d={geo.u.line} fill="none" stroke={up.color} strokeWidth={1.8} strokeLinejoin="round" />
            <path d={geo.d.line} fill="none" stroke={down.color} strokeWidth={1.8} strokeLinejoin="round" />
            {geo.xt.map((t, i) => (
              <text key={i} x={geo.X(t)} y={H - 5} fontSize={10.5} fill="var(--fg-3)" className="font-mono" textAnchor={i === 0 ? "start" : i === geo.xt.length - 1 ? "end" : "middle"}>
                {timeLabel(t, geo.span)}
              </text>
            ))}
            {hover !== null && points[hover] && <line x1={geo.X(points[hover].ts)} x2={geo.X(points[hover].ts)} y1={pad} y2={H - axis - pad} stroke="var(--fg-3)" />}
            <rect width={W} height={H} fill="transparent" onPointerMove={move} onPointerLeave={() => { setHover(null); tip.hide(); }} />
          </svg>
        )}
      </div>
    </div>
  );
}

/* ---------------- per-core capsules (after the finance stocks card) ---------------- */

export function CoreColumns({ cores, height = 110 }: { cores: number[]; height?: number }) {
  const tip = useTip();
  return (
    <div className="flex items-end gap-[5px]" style={{ height }} role="img" aria-label={`Per-core CPU: ${cores.map((c) => Math.round(c) + "%").join(", ")}`}>
      {cores.map((c, i) => (
        <div
          key={i}
          className="relative flex h-full min-w-[6px] max-w-[22px] flex-1 items-end overflow-hidden rounded-full bg-surface-3"
          onPointerEnter={(e) => tip.show(e.clientX, e.clientY, { title: `Core ${i}`, rows: [{ label: "Usage", value: `${c.toFixed(0)}%`, color: "var(--series)" }] })}
          onPointerLeave={tip.hide}
        >
          <div className="w-full rounded-full transition-[height] duration-700 [transition-timing-function:cubic-bezier(.32,.72,0,1)]" style={{ height: `${Math.max(4, c)}%`, background: c >= 90 ? "var(--warn)" : "var(--series)" }} />
        </div>
      ))}
    </div>
  );
}
