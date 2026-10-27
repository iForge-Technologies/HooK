import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { useReducedMotion, useSize } from "../lib/hooks";
import { niceStep, smooth } from "./charts";
import { useTip, type TipRow } from "./overlay";

/* Components ported from the finance and analytics bento references and
   pointed at monitoring data. Marks on gradient cards are tones of white, as in
   the finance bento; status colours there are fixed because the cards keep the
   same background in both themes. */

export const ON_GRADIENT = { up: "#46e08a", warn: "#f2a516", down: "#ff6b72", muted: "rgba(255,255,255,.35)" };

const ease = (t: number) => 1 - Math.pow(1 - t, 3);

export function GradientCard({ tone, className = "", children, label }: { tone: "blue" | "graph" | "pink" | "copper" | "green" | "lime"; className?: string; children: ReactNode; label: string }) {
  return (
    <section aria-label={label} className={`hero-card g-${tone} rim enter relative min-w-0 overflow-hidden rounded-[28px] p-5 sm:p-6 ${className}`}>
      {children}
    </section>
  );
}

/* ---------------- dot-matrix number (finance net-worth counter) ---------------- */

const GLYPHS: Record<string, string[]> = {
  "0": ["01110", "10001", "10011", "10101", "11001", "10001", "01110"],
  "1": ["00100", "01100", "00100", "00100", "00100", "00100", "01110"],
  "2": ["01110", "10001", "00001", "00010", "00100", "01000", "11111"],
  "3": ["11111", "00010", "00100", "00010", "00001", "10001", "01110"],
  "4": ["00010", "00110", "01010", "10010", "11111", "00010", "00010"],
  "5": ["11111", "10000", "11110", "00001", "00001", "10001", "01110"],
  "6": ["00110", "01000", "10000", "11110", "10001", "10001", "01110"],
  "7": ["11111", "00001", "00010", "00100", "01000", "01000", "01000"],
  "8": ["01110", "10001", "10001", "01110", "10001", "10001", "01110"],
  "9": ["01110", "10001", "10001", "01111", "00001", "00010", "01100"],
  "%": ["11001", "11010", "00010", "00100", "01000", "01011", "10011"],
  ".": ["0", "0", "0", "0", "0", "0", "1"],
  "/": ["00001", "00010", "00010", "00100", "01000", "01000", "10000"],
  "-": ["000", "000", "000", "111", "000", "000", "000"],
  " ": ["00", "00", "00", "00", "00", "00", "00"],
};

/** Counts up to `value` and draws it in 5×7 dots. `aria-label` carries the text. */
export function DotNumber({ value, format, pitch = 7, cols = 40 }: { value: number | null; format: (v: number) => string; pitch?: number; cols?: number }) {
  const reduce = useReducedMotion();
  const [shown, setShown] = useState(value ?? 0);
  const from = useRef(0);
  useEffect(() => {
    if (value == null) return;
    if (reduce) {
      setShown(value);
      from.current = value;
      return;
    }
    const a = from.current, t0 = performance.now();
    let raf = 0;
    const f = (now: number) => {
      const t = Math.min(1, (now - t0) / 900);
      const v = a + (value - a) * ease(t);
      setShown(v);
      from.current = v;
      if (t < 1) raf = requestAnimationFrame(f);
    };
    raf = requestAnimationFrame(f);
    return () => cancelAnimationFrame(raf);
  }, [value, reduce]);

  const text = value == null ? "-" : format(shown);
  const { on, used } = useMemo(() => {
    const on = new Set<string>();
    let x = 0;
    for (const ch of text) {
      const g = GLYPHS[ch] ?? GLYPHS[" "];
      for (let r = 0; r < 7; r++) for (let k = 0; k < g[r].length; k++) if (g[r][k] === "1") on.add(`${x + k},${r}`);
      x += g[0].length + 1;
    }
    return { on, used: Math.max(1, x - 1) };
  }, [text]);
  const width = Math.max(used, 1);
  return (
    <svg width={width * pitch} height={7 * pitch} viewBox={`0 0 ${width * pitch} ${7 * pitch}`} role="img" aria-label={value == null ? "no data" : format(value)} className="block max-w-full">
      {Array.from({ length: Math.min(cols, width) * 7 }, (_, i) => {
        const c = Math.floor(i / 7), r = i % 7, lit = on.has(`${c},${r}`);
        return <circle key={i} cx={c * pitch + pitch / 2} cy={r * pitch + pitch / 2} r={pitch * 0.36} fill="#fff" opacity={lit ? 1 : 0.08} style={{ transition: "opacity .26s", transitionDelay: `${c * 7}ms` }} />;
      })}
    </svg>
  );
}

/* ---------------- half gauge of ticks (finance allocation) ---------------- */

export type GaugeSegment = { label: string; value: number; color: string };

export function HalfGauge({ segments, center, sub }: { segments: GaugeSegment[]; center: string; sub: string }) {
  const total = segments.reduce((a, s) => a + s.value, 0) || 1;
  const n = 56;
  const P = (r: number, a: number) => [r * Math.cos((a * Math.PI) / 180), r * Math.sin((a * Math.PI) / 180)];
  const ticks: { x1: number; y1: number; x2: number; y2: number; color: string }[] = [];
  for (let i = 0; i <= n; i++) {
    const f = i / n;
    let acc = 0, color = segments[segments.length - 1]?.color ?? ON_GRADIENT.muted;
    for (const s of segments) {
      acc += s.value / total;
      if (f <= acc + 1e-9) {
        color = s.color;
        break;
      }
    }
    const a = 180 + f * 180;
    const [x1, y1] = P(136, a), [x2, y2] = P(i % 7 === 0 ? 112 : 118, a);
    ticks.push({ x1, y1, x2, y2, color: total === 1 && segments.every((s) => !s.value) ? ON_GRADIENT.muted : color });
  }
  return (
    <svg viewBox="-150 -150 300 168" className="mx-auto block w-full max-w-[300px]" role="img" aria-label={segments.map((s) => `${s.label} ${s.value}`).join(", ")}>
      {ticks.map((t, i) => (
        <line key={i} x1={t.x1} y1={t.y1} x2={t.x2} y2={t.y2} stroke={t.color} strokeWidth={3.2} strokeLinecap="round" style={{ animation: `tick-in .4s ${i * 12}ms both` }} />
      ))}
      <text y={-22} textAnchor="middle" fontWeight={200} fontSize={50} fill="#fff" letterSpacing={-2} className="tnum">
        {center}
      </text>
      <text y={2} textAnchor="middle" fontSize={12} fontWeight={500} fill="rgba(255,255,255,.55)">
        {sub}
      </text>
      <style>{"@keyframes tick-in{from{opacity:0}}"}</style>
    </svg>
  );
}

/* ---------------- stream band (finance net-worth stream) ---------------- */

export type BandPoint = { ts: number; v: number; bad: number; label: string; tip: TipRow[] };

/** A band symmetric around the centre line; thickness is the value. Buckets
 *  with failures get a marker on the baseline. */
export function StreamBand({ points, fmt, height = 210 }: { points: BandPoint[]; fmt: (v: number) => string; height?: number }) {
  const [ref, { w: W }] = useSize();
  const tip = useTip();
  const id = useId().replace(/:/g, "");
  const reduce = useReducedMotion();
  const [grow, setGrow] = useState(reduce ? 1 : 0);
  const [hover, setHover] = useState<number | null>(null);
  const H = height, top = 26, bot = 30, mid = top + (H - top - bot) / 2, half = (H - top - bot) / 2;
  // Re-run the grow-in only when the series itself changes (new range or
  // bucket), not on every live re-render of the page.
  const shape = `${points.length}:${points[0]?.ts ?? 0}`;

  useEffect(() => {
    if (reduce) return setGrow(1);
    setGrow(0);
    const t0 = performance.now();
    let raf = 0;
    const f = (now: number) => {
      const t = Math.min(1, (now - t0) / 1100);
      setGrow(ease(t));
      if (t < 1) raf = requestAnimationFrame(f);
    };
    raf = requestAnimationFrame(f);
    return () => cancelAnimationFrame(raf);
  }, [shape, reduce]);

  const geo = useMemo(() => {
    if (!W || points.length < 2) return null;
    const n = points.length;
    const hi = Math.max(...points.map((p) => p.v), 1);
    const X = (i: number) => -16 + (i / (n - 1)) * (W + 32);
    const thick = (v: number) => Math.max(1.5, (v / hi) * half * 0.92);
    const T = points.map((p, i) => [X(i), mid - thick(p.v) * grow] as [number, number]);
    const B = points.map((p, i) => [X(i), mid + thick(p.v) * grow] as [number, number]).reverse();
    const band = smooth(T) + smooth(B).replace(/^M/, "L") + "Z";
    const inner = points.map((p, i) => [X(i), mid - thick(p.v) * 0.45 * grow] as [number, number]);
    const innerB = points.map((p, i) => [X(i), mid + thick(p.v) * 0.45 * grow] as [number, number]).reverse();
    const core = smooth(inner) + smooth(innerB).replace(/^M/, "L") + "Z";
    // three pinned values like the finance stream; short histories collapse duplicates
    const marks = [...new Set([Math.round((n - 1) * 0.18), Math.round((n - 1) * 0.55), n - 1])];
    return { X, band, core, marks, thick };
  }, [W, points, grow, half, mid]);

  const move = (e: React.PointerEvent<SVGRectElement>) => {
    if (!geo) return;
    const r = e.currentTarget.ownerSVGElement!.getBoundingClientRect();
    const i = Math.max(0, Math.min(points.length - 1, Math.round(((e.clientX - r.left + 16) / (W + 32)) * (points.length - 1))));
    setHover(i);
    const p = points[i];
    tip.show(r.left + geo.X(i), r.top + mid - geo.thick(p.v), { title: p.label, rows: p.tip });
  };

  return (
    <div ref={ref} className="absolute inset-x-0 bottom-0" style={{ height: H }}>
      {geo && (
        <svg width={W} height={H} className="block" aria-hidden>
          <defs>
            <linearGradient id={`${id}f`} x1="0" x2="1">
              <stop offset="0" stopColor="#fff" stopOpacity="0" />
              <stop offset=".22" stopColor="#fff" stopOpacity="1" />
            </linearGradient>
            <mask id={`${id}m`}>
              <rect width={W} height={H} fill={`url(#${id}f)`} />
            </mask>
          </defs>
          <g mask={`url(#${id}m)`}>
            <path d={geo.band} fill="#A9B2FF" opacity={0.9} />
            <path d={geo.core} fill="#fff" opacity={0.95} />
          </g>
          {points.map((p, i) =>
            p.bad > 0 ? <circle key={i} cx={Math.min(W - 8, Math.max(8, geo.X(i)))} cy={H - bot + 12} r={Math.min(6, 2.5 + Math.log2(p.bad + 1))} fill={ON_GRADIENT.down} stroke="rgba(0,0,0,.35)" /> : null,
          )}
          {geo.marks.map((i, k) => {
            const x = Math.min(W - 40, Math.max(40, geo.X(i)));
            return (
              <g key={k} style={{ opacity: grow > 0.6 ? 1 : 0, transition: `opacity .4s ${k * 0.08}s` }}>
                <line x1={x} x2={x} y1={top - 4} y2={H - bot + 2} stroke="rgba(0,0,0,.5)" strokeWidth={1.5} />
                <rect x={x - 36} y={top - 14} width={72} height={22} rx={11} fill="#000" />
                <text x={x} y={top + 1} textAnchor="middle" fontSize={11} fontWeight={600} fill="#fff" className="font-mono">
                  {fmt(points[i].v)}
                </text>
              </g>
            );
          })}
          {hover !== null && <line x1={geo.X(hover)} x2={geo.X(hover)} y1={top} y2={H - bot} stroke="#fff" strokeWidth={1.5} />}
          <rect width={W} height={H} fill="transparent" onPointerMove={move} onPointerLeave={() => { setHover(null); tip.hide(); }} />
        </svg>
      )}
    </div>
  );
}

/* ---------------- mirrored waves (finance cash flow) ---------------- */

export type WavePoint = { ts: number; a: number; b: number; label: string };

export function Waves({ points, fmt, labels, height = 230 }: { points: WavePoint[]; fmt: (v: number) => string; labels: [string, string]; height?: number }) {
  const [ref, { w: W }] = useSize();
  const tip = useTip();
  const [hover, setHover] = useState<number | null>(null);
  const H = height, L = 52, gap = 14;

  const geo = useMemo(() => {
    if (!W || points.length < 2) return null;
    const R = W - 20, mid = H / 2, amp = mid - gap - 8;
    const hi = Math.max(1, ...points.map((p) => Math.max(p.a, p.b)));
    const step = niceStep(hi, 2);
    const max = Math.ceil(hi / step) * step;
    const X = (i: number) => L + (i / (points.length - 1)) * (R - L);
    const yA = (v: number) => mid - gap - (v / max) * amp;
    const yB = (v: number) => mid + gap + (v / max) * amp;
    const dense = points.length > 120;
    const line = (P: [number, number][]) => (dense ? "M" + P.map((p) => p.join(",")).join("L") : smooth(P));
    return {
      X, R, mid, max, yA, yB,
      a: line(points.map((p, i) => [X(i), yA(p.a)])),
      b: line(points.map((p, i) => [X(i), yB(p.b)])),
      grid: [max / 2, max],
    };
  }, [W, H, points]);

  const move = (e: React.PointerEvent<SVGRectElement>) => {
    if (!geo) return;
    const r = e.currentTarget.ownerSVGElement!.getBoundingClientRect();
    const i = Math.max(0, Math.min(points.length - 1, Math.round(((e.clientX - r.left - L) / (geo.R - L)) * (points.length - 1))));
    setHover(i);
    const p = points[i];
    tip.show(r.left + geo.X(i), r.top + geo.yA(p.a), {
      title: p.label,
      rows: [
        { label: labels[0], value: fmt(p.a), color: "#fff" },
        { label: labels[1], value: fmt(p.b), color: "#FFD0E6" },
      ],
    });
  };

  return (
    <div ref={ref} className="w-full" style={{ height: H }}>
      {geo && (
        <svg width={W} height={H} className="block" aria-label={`${labels[0]} above, ${labels[1]} below`} role="img">
          {geo.grid.map((v) => (
            <g key={v}>
              <line x1={L} x2={geo.R} y1={geo.yA(v)} y2={geo.yA(v)} stroke="rgba(255,255,255,.14)" />
              <line x1={L} x2={geo.R} y1={geo.yB(v)} y2={geo.yB(v)} stroke="rgba(255,255,255,.14)" />
              <text x={L - 8} y={geo.yA(v) + 3.5} textAnchor="end" fontSize={10} fill="rgba(255,255,255,.6)" className="font-mono">
                {fmt(v)}
              </text>
              <text x={L - 8} y={geo.yB(v) + 3.5} textAnchor="end" fontSize={10} fill="rgba(255,255,255,.6)" className="font-mono">
                {fmt(v)}
              </text>
            </g>
          ))}
          <path d={geo.a} fill="none" stroke="#fff" strokeWidth={2.2} strokeLinecap="round" />
          <path d={geo.b} fill="none" stroke="#FFD0E6" strokeWidth={2.2} strokeLinecap="round" />
          {[0, Math.floor((points.length - 1) / 2), points.length - 1].map((i) => (
            <text key={i} x={geo.X(i)} y={geo.mid + 4} textAnchor={i === 0 ? "start" : i === points.length - 1 ? "end" : "middle"} fontSize={10.5} fontWeight={500} fill="rgba(255,255,255,.8)" className="font-mono">
              {points[i].label}
            </text>
          ))}
          {hover !== null && <line x1={geo.X(hover)} x2={geo.X(hover)} y1={8} y2={H - 8} stroke="rgba(255,255,255,.5)" />}
          <rect x={L - 8} width={geo.R - L + 16} height={H} fill="transparent" onPointerMove={move} onPointerLeave={() => { setHover(null); tip.hide(); }} />
        </svg>
      )}
    </div>
  );
}

/* ---------------- capsule columns (finance stocks) ---------------- */

export function Capsules({ items, height = 150, fmtTip }: { items: { label: string; value: number; highlight?: boolean }[]; height?: number; fmtTip: (v: number) => string }) {
  const tip = useTip();
  const reduce = useReducedMotion();
  const [up, setUp] = useState(reduce);
  useEffect(() => {
    const r = requestAnimationFrame(() => requestAnimationFrame(() => setUp(true)));
    return () => cancelAnimationFrame(r);
  }, []);
  const max = Math.max(1, ...items.map((i) => i.value));
  return (
    <div className="flex items-end justify-between gap-1.5" style={{ height }} role="img" aria-label={items.map((i) => `${i.label} ${fmtTip(i.value)}`).join(", ")}>
      {items.map((it, i) => (
        <div
          key={i}
          className="flex h-full min-w-0 flex-1 flex-col items-center gap-1.5"
          onPointerEnter={(e) => tip.show(e.clientX, e.clientY, { title: it.label, rows: [{ label: "Incidents", value: fmtTip(it.value), color: "#fff" }] })}
          onPointerLeave={tip.hide}
        >
          <div className="relative flex w-full max-w-[22px] flex-1 items-end overflow-hidden rounded-full bg-black/25">
            <div
              className="w-full rounded-full transition-[height] duration-700 [transition-timing-function:cubic-bezier(.32,.72,0,1)]"
              style={{ height: up && it.value ? `${Math.max(12, (it.value / max) * 100)}%` : "0%", background: it.highlight ? "#E8FF1F" : "#fff", transitionDelay: `${i * 70}ms` }}
            />
          </div>
          <span className={`font-mono text-[10px] ${it.highlight ? "text-white" : "text-white/55"}`}>{it.label}</span>
        </div>
      ))}
    </div>
  );
}

/* ---------------- tick strip (finance runway / goals) ---------------- */

export function TickStrip({ fraction, n = 36, color = "#E8FF1F", off = "rgba(255,255,255,.28)", height = 20 }: { fraction: number; n?: number; color?: string; off?: string; height?: number }) {
  return (
    <div className="flex items-end justify-between" style={{ height }} aria-hidden>
      {Array.from({ length: n }, (_, i) => (
        <span key={i} className="w-[2px] rounded-full" style={{ height: i % 6 === 0 ? "100%" : "60%", background: i / (n - 1) <= fraction ? color : off, transition: `background .2s ${i * 14}ms` }} />
      ))}
    </div>
  );
}

/* ---------------- treemap (analytics categories) ---------------- */

type Rect<T> = T & { x: number; y: number; w: number; h: number };

function squarify<T extends { v: number }>(items: T[], x: number, y: number, w: number, h: number): Rect<T>[] {
  const total = items.reduce((a, b) => a + b.v, 0) || 1, scale = (w * h) / total, out: Rect<T>[] = [];
  let rest = items.map((i) => ({ ...i, a: i.v * scale }));
  const worst = (row: typeof rest, len: number) => {
    const s = row.reduce((a, b) => a + b.a, 0), mx = Math.max(...row.map((r) => r.a)), mn = Math.min(...row.map((r) => r.a));
    return Math.max((len * len * mx) / (s * s), (s * s) / (len * len * mn));
  };
  while (rest.length) {
    const len = Math.min(w, h);
    const row = [rest[0]];
    let i = 1;
    while (i < rest.length && worst([...row, rest[i]], len) <= worst(row, len)) row.push(rest[i++]);
    rest = rest.slice(i);
    const s = row.reduce((a, b) => a + b.a, 0);
    if (w >= h) {
      const cw = s / h;
      let cy = y;
      for (const r of row) {
        const rh = r.a / cw;
        out.push({ ...r, x, y: cy, w: cw, h: rh });
        cy += rh;
      }
      x += cw;
      w -= cw;
    } else {
      const rh = s / w;
      let cx = x;
      for (const r of row) {
        const rw = r.a / rh;
        out.push({ ...r, x: cx, y, w: rw, h: rh });
        cx += rw;
      }
      y += rh;
      h -= rh;
    }
  }
  return out;
}

export type TreeItem = { key: string; name: string; group: string; v: number };
export type TreeGroup = { id: string; name: string; color: string; ink: string };

export function Treemap({ items, groups, fmt, height = 320 }: { items: TreeItem[]; groups: TreeGroup[]; fmt: (v: number) => string; height?: number }) {
  const [ref, { w: W }] = useSize();
  const tip = useTip();
  const [placed, setPlaced] = useState(false);
  useEffect(() => {
    if (!W) return;
    const r = requestAnimationFrame(() => requestAnimationFrame(() => setPlaced(true)));
    return () => cancelAnimationFrame(r);
  }, [W]);
  const G = 6, H = height;
  const total = items.reduce((a, b) => a + b.v, 0) || 1;
  const rects = W ? squarify([...items].sort((a, b) => b.v - a.v), 0, 0, W + G, H + G) : [];
  return (
    <div ref={ref} className="relative" style={{ height: H }} role="img" aria-label={items.slice(0, 8).map((i) => `${i.name} on ${groups.find((g) => g.id === i.group)?.name}: ${fmt(i.v)}`).join("; ")}>
      {rects.map((r) => {
        const g = groups.find((x) => x.id === r.group) ?? groups[groups.length - 1];
        const w = r.w - G, h = r.h - G;
        const share = ((r.v / total) * 100).toFixed(1).replace(/\.0$/, "") + "%";
        const showName = w >= 70 && h >= 44, showVal = w >= 70 && h >= 66;
        return (
          <div
            key={r.key}
            className="absolute flex flex-col justify-between overflow-hidden rounded-[14px] p-2.5 transition-[left,top,width,height] duration-700 [transition-timing-function:cubic-bezier(.2,.9,.25,1)] hover:brightness-110 motion-reduce:transition-none"
            style={{ left: placed ? r.x : W / 2, top: placed ? r.y : H / 2, width: placed ? Math.max(0, w) : 0, height: placed ? Math.max(0, h) : 0, background: g.color, color: g.ink }}
            onPointerMove={(e) => tip.show(e.clientX, e.clientY, { title: `${r.name} · ${g.name}`, rows: [{ label: "Memory", value: fmt(r.v), color: g.color }, { label: "Share of shown", value: share }] })}
            onPointerLeave={tip.hide}
          >
            {showName && <span className="truncate text-[11px] font-semibold">{r.name}</span>}
            {showVal && <span className="tnum truncate font-mono text-lg leading-none font-medium tracking-[-0.04em]">{fmt(r.v)}</span>}
          </div>
        );
      })}
    </div>
  );
}

/* ---------------- radar (analytics seasonality) ---------------- */

/** A smooth closed shape through `values` around a dial (low-order Fourier fit,
 *  so lobes stay round). Hover snaps to the nearest axis. */
export function Radar({ values, axis, color, fmt, label }: { values: number[]; axis: string[]; color: string; fmt: (v: number, i: number) => TipRow[]; label: string }) {
  const [ref, { w }] = useSize();
  const tip = useTip();
  const [hover, setHover] = useState<number | null>(null);
  const N = values.length, S = Math.min(w, 380), R = S / 2 - 30, C = S / 2;
  const ang = (i: number) => -Math.PI / 2 + (i / N) * Math.PI * 2;
  const fit = useMemo(() => {
    const lo = Math.min(...values), hi = Math.max(...values);
    const norm = values.map((x) => (hi === lo ? 0.5 : 0.25 + ((x - lo) / (hi - lo)) * 0.75));
    const K = Math.min(5, Math.floor(N / 2)), co: [number, number][] = [];
    for (let k = 0; k <= K; k++) {
      let a = 0, b = 0;
      norm.forEach((x, i) => {
        const th = (i / N) * Math.PI * 2;
        a += x * Math.cos(k * th);
        b += x * Math.sin(k * th);
      });
      co.push([(a * (k ? 2 : 1)) / N, (b * (k ? 2 : 1)) / N]);
    }
    return (th: number) => co.reduce((r, [a, b], k) => r + a * Math.cos(k * th) + b * Math.sin(k * th), 0);
  }, [values, N]);
  const at = (th: number): [number, number] => {
    const v = Math.max(0.05, Math.min(1.05, fit(th))), a = -Math.PI / 2 + th;
    return [C + Math.cos(a) * (R * 0.2 + v * R * 0.8), C + Math.sin(a) * (R * 0.2 + v * R * 0.8)];
  };
  let d = "";
  for (let j = 0; j <= 180; j++) {
    const p = at((j / 180) * Math.PI * 2);
    d += (j ? "L" : "M") + p[0].toFixed(1) + "," + p[1].toFixed(1);
  }
  const move = (e: React.PointerEvent<SVGRectElement>) => {
    const r = e.currentTarget.ownerSVGElement!.getBoundingClientRect(), x = e.clientX - r.left - C, y = e.clientY - r.top - C;
    if (Math.hypot(x, y) < R * 0.2) {
      setHover(null);
      tip.hide();
      return;
    }
    let a = Math.atan2(y, x) + Math.PI / 2;
    if (a < 0) a += Math.PI * 2;
    const i = Math.round((a / (Math.PI * 2)) * N) % N;
    const p = at((i / N) * Math.PI * 2);
    setHover(i);
    tip.show(r.left + p[0], r.top + p[1], { title: axis[i], rows: fmt(values[i], i) });
  };
  const hp = hover !== null ? at((hover / N) * Math.PI * 2) : null;
  return (
    <div ref={ref} className="mx-auto w-full max-w-[380px]" style={{ height: S || 300 }}>
      {S > 0 && (
        <svg width={S} height={S} className="mx-auto block" role="img" aria-label={label}>
          {[0.33, 0.66, 1].map((k) => (
            <circle key={k} cx={C} cy={C} r={R * k} fill="none" stroke="var(--line)" />
          ))}
          {axis.map((m, i) => (
            <g key={i}>
              <line x1={C + Math.cos(ang(i)) * R * 0.2} y1={C + Math.sin(ang(i)) * R * 0.2} x2={C + Math.cos(ang(i)) * R} y2={C + Math.sin(ang(i)) * R} stroke="var(--line)" />
              {i % Math.ceil(N / 8) === 0 && (
                <text x={C + Math.cos(ang(i)) * (R + 16)} y={C + Math.sin(ang(i)) * (R + 16)} textAnchor="middle" dominantBaseline="central" fontSize={10.5} fill="var(--fg-3)" className="font-mono">
                  {m}
                </text>
              )}
            </g>
          ))}
          <path d={d + "Z"} fill={color} fillOpacity={0.12} stroke={color} strokeWidth={2.4} strokeLinejoin="round" />
          <circle cx={C} cy={C} r={R * 0.2} fill="var(--surface)" stroke="var(--line)" />
          {hp && <circle cx={hp[0]} cy={hp[1]} r={4.5} fill={color} stroke="var(--surface)" strokeWidth={2} />}
          <rect width={S} height={S} fill="transparent" onPointerMove={move} onPointerLeave={() => { setHover(null); tip.hide(); }} />
        </svg>
      )}
    </div>
  );
}
