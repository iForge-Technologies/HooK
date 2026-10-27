import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "wouter";
import { Capsules, DotNumber, GradientCard, HalfGauge, ON_GRADIENT, Radar, StreamBand, TickStrip, Treemap, Waves, type BandPoint, type TreeGroup } from "../components/bento";
import { HeartbeatBar } from "../components/charts";
import { shownStatus, StatusIcon } from "../components/status";
import { Segmented, Spinner } from "../components/ui";
import { api, type MonitorListItem, type Server } from "../lib/api";
import { daysUntil, fmtBytes, fmtDateTime, fmtDuration, fmtMs, fmtPct, fmtRate, fmtUptime, memPct } from "../lib/format";
import { useNow } from "../lib/hooks";

type Range = "24h" | "7d" | "30d";

interface Overview {
  range: Range;
  from: number;
  to: number;
  bucket: number;
  uptime: number | null;
  uptimePrev: number | null;
  checks: { up: number; down: number };
  latency: { ts: number; latency: number; up: number; down: number; degraded: number }[];
  feed: { source: "monitor" | "server"; id: number; name: string; kind: string; startedAt: number; resolvedAt: number | null; detail: string }[];
  network: { ts: number; rx: number; tx: number }[];
  memProcs: { hostId: number; host: string; name: string; rss: number }[];
}

const RANGE_WORDS: Record<Range, string> = { "24h": "24 hours", "7d": "7 days", "30d": "30 days" };
const KIND_LABEL: Record<string, string> = { down: "Down", offline: "Offline", cpu: "High CPU", mem: "High memory", disk: "Disk filling", container: "Container problem", service: "Service down", forecast: "Disk full soon" };

// Categorical slots (validated order) for servers in the treemap; the rest fold into "Other".
const SLOTS = [
  { color: "var(--cat-1)", ink: "#fff" },
  { color: "var(--cat-2)", ink: "#fff" },
  { color: "var(--cat-3)", ink: "#fff" },
  { color: "var(--cat-4)", ink: "#0b0b0c" },
];

const bucketLabel = (ts: number, range: Range) =>
  range === "24h"
    ? new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : new Date(ts).toLocaleString([], { month: "short", day: "numeric", hour: range === "7d" ? "2-digit" : undefined });

const uptimeText = (v: number) => (v >= 99.995 ? "100%" : (Math.floor(v * 100) / 100).toFixed(2) + "%");

export function OverviewPage() {
  const [range, setRange] = useState<Range>("24h");
  const now = useNow(30_000);
  const monitors = useQuery({ queryKey: ["monitors"], queryFn: () => api.get<MonitorListItem[]>("/monitors"), refetchInterval: 60_000 });
  const servers = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers"), refetchInterval: 60_000 });
  const ov = useQuery({ queryKey: ["overview", range], queryFn: () => api.get<Overview>(`/overview?range=${range}`), refetchInterval: 30_000, placeholderData: (p) => p });

  const ms = monitors.data ?? [];
  const ss = servers.data ?? [];
  const o = ov.data;

  const counts = useMemo(() => {
    const active = ms.filter((m) => m.active);
    const c = { up: 0, warn: 0, down: 0, maint: 0, paused: ms.length - active.length };
    for (const m of active) {
      if (m.status === "up") c.up++;
      else if (m.status === "down") c.down++;
      else if (m.status === "maintenance") c.maint++;
      else c.warn++;
    }
    return c;
  }, [ms]);

  if (monitors.isPending || servers.isPending || !o) {
    return (
      <div className="grid h-64 place-items-center text-fg-2">
        <Spinner className="size-5" />
      </div>
    );
  }

  const words = RANGE_WORDS[range];
  const delta = o.uptime != null && o.uptimePrev != null ? (o.uptime - o.uptimePrev) * 100 : null;

  // hero band
  const band: BandPoint[] = o.latency.map((b) => ({
    ts: b.ts,
    v: b.latency,
    bad: b.down,
    label: bucketLabel(b.ts, range),
    tip: [
      { label: "Avg response", value: fmtMs(b.latency), color: "#A9B2FF" },
      { label: "Checks", value: String(b.up + b.down) },
      ...(b.down ? [{ label: "Failed", value: String(b.down), color: ON_GRADIENT.down }] : []),
      ...(b.degraded ? [{ label: "Slow", value: String(b.degraded), color: ON_GRADIENT.warn }] : []),
    ],
  }));

  // incidents
  const inRange = o.feed.filter((f) => f.startedAt >= o.from);
  const resolved = o.feed.filter((f) => f.resolvedAt != null && f.resolvedAt >= o.from);
  const mttr = resolved.length ? resolved.reduce((a, f) => a + (f.resolvedAt! - f.startedAt), 0) / resolved.length : null;
  const openNow = o.feed.filter((f) => f.resolvedAt == null);

  // certificates
  const certs = ms
    .filter((m) => m.certExpires > 0)
    .map((m) => ({ id: m.id, name: m.name, expires: m.certExpires, days: daysUntil(m.certExpires) }))
    .sort((a, b) => a.expires - b.expires);
  const soonest = certs[0];

  // servers
  const online = ss.filter((s) => s.status === "online" && s.metrics);
  const avg = (f: (s: Server) => number) => (online.length ? online.reduce((a, s) => a + f(s), 0) / online.length : null);
  const memUsed = online.reduce((a, s) => a + s.metrics!.memUsed, 0);
  const memTotal = online.reduce((a, s) => a + s.metrics!.memTotal, 0);

  // incidents per bucket for the capsules: 8 buckets across the range
  const nCaps = range === "7d" ? 7 : range === "30d" ? 10 : 8;
  const capW = (o.to - o.from) / nCaps;
  const caps = Array.from({ length: nCaps }, (_, i) => {
    const start = o.from + i * capW;
    return {
      label:
        range === "7d"
          ? new Date(start + capW / 2).toLocaleDateString([], { weekday: "short" }).slice(0, 2)
          : range === "24h"
            ? new Date(start + capW).toLocaleTimeString([], { hour: "2-digit" }).replace(/\s?[AP]M/i, "")
            : new Date(start + capW / 2).toLocaleDateString([], { day: "numeric", month: "numeric" }),
      value: inRange.filter((f) => f.startedAt >= start && f.startedAt < start + capW).length,
      highlight: i === nCaps - 1,
    };
  });

  // radar: incident starts by local hour of day
  const byHour = Array.from({ length: 24 }, () => ({ total: 0, monitor: 0, server: 0 }));
  for (const f of inRange) {
    const h = new Date(f.startedAt).getHours();
    byHour[h].total++;
    byHour[h][f.source]++;
  }
  const busiest = byHour.reduce((bi, h, i) => (h.total > byHour[bi].total ? i : bi), 0);

  // treemap: top servers by memory shown, rest folded
  const hostTotals = new Map<number, { name: string; v: number }>();
  for (const p of o.memProcs) hostTotals.set(p.hostId, { name: p.host, v: (hostTotals.get(p.hostId)?.v ?? 0) + p.rss });
  const topHosts = [...hostTotals.entries()].sort((a, b) => b[1].v - a[1].v);
  const groups: TreeGroup[] = topHosts.slice(0, SLOTS.length).map(([id, h], i) => ({ id: String(id), name: h.name, ...SLOTS[i] }));
  if (topHosts.length > SLOTS.length) groups.push({ id: "other", name: "Other servers", color: "var(--surface-3)", ink: "var(--fg)" });
  const treeItems = o.memProcs.map((p, i) => ({
    key: `${p.hostId}-${p.name}-${i}`,
    name: p.name,
    group: groups.some((g) => g.id === String(p.hostId)) ? String(p.hostId) : "other",
    v: p.rss,
  }));

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="mr-auto">
          <h1 className="text-[28px] font-bold tracking-[-0.03em]">
            Overview
            <span className="ml-3 text-[15px] font-medium tracking-[-0.01em] text-fg-3">last {words}</span>
          </h1>
        </div>
        <Segmented label="Period" value={range} onChange={setRange} items={(["24h", "7d", "30d"] as const).map((r) => ({ value: r, label: r }))} />
      </div>

      <div className="grid grid-cols-12 gap-3.5">
        {/* hero: uptime + response stream */}
        <GradientCard tone="blue" label="Uptime" className="col-span-12 min-h-[376px] lg:col-span-8">
          <div className="relative z-10 flex flex-wrap items-center justify-between gap-3">
            <div className="text-lg font-semibold tracking-[-0.02em]">Uptime</div>
            <div className="flex gap-4 text-xs font-medium text-white/70">
              <span className="inline-flex items-center gap-1.5">
                <i className="size-2 rounded-full bg-white" />
                Avg response
              </span>
              <span className="inline-flex items-center gap-1.5">
                <i className="size-2 rounded-full" style={{ background: ON_GRADIENT.down }} />
                Failed checks
              </span>
            </div>
          </div>
          <div className="relative z-10 mt-4 flex flex-wrap items-end gap-x-4 gap-y-2">
            <DotNumber value={o.uptime == null ? null : o.uptime * 100} format={uptimeText} pitch={typeof window !== "undefined" && window.innerWidth < 520 ? 5.2 : 7} />
            <div className="pb-1">
              {delta != null && (
                <div className="font-mono text-[12.5px] font-semibold" style={{ color: delta >= 0 ? "#E8FF1F" : "#FFD0E6" }}>
                  {delta >= 0 ? "↑" : "↓"} {Math.abs(delta).toFixed(2)} pts
                </div>
              )}
              <div className="mt-0.5 text-[12.5px] font-medium text-white/60">
                {(o.checks.up + o.checks.down).toLocaleString()} checks · {o.checks.down.toLocaleString()} failed
              </div>
            </div>
          </div>
          {band.length > 1 ? (
            <StreamBand points={band} fmt={fmtMs} />
          ) : (
            <p className="absolute inset-x-6 bottom-10 text-sm text-white/70">Not enough history to draw {words} yet. Try a shorter period.</p>
          )}
        </GradientCard>

        {/* status gauge */}
        <GradientCard tone="graph" label="Monitors by status" className="col-span-12 flex min-h-[376px] flex-col sm:col-span-6 lg:col-span-4">
          <div className="flex items-center justify-between">
            <div className="text-lg font-semibold tracking-[-0.02em]">Monitors</div>
            <Link href="/monitors" className="rounded-full bg-white/12 px-2.5 py-1 font-mono text-[11px] font-semibold hover:bg-white/20">
              {ms.length} total →
            </Link>
          </div>
          <div className="mt-6">
            <HalfGauge
              center={`${counts.up}/${ms.length - counts.paused}`}
              sub="up right now"
              segments={[
                { label: "Up", value: counts.up, color: ON_GRADIENT.up },
                { label: "Slow or retrying", value: counts.warn, color: ON_GRADIENT.warn },
                { label: "Down", value: counts.down, color: ON_GRADIENT.down },
                { label: "Maintenance", value: counts.maint, color: "#A9B2FF" },
                { label: "Paused", value: counts.paused, color: ON_GRADIENT.muted },
              ]}
            />
          </div>
          <ul className="mt-auto text-[13px]">
            {(
              [
                ["Up", counts.up, "up", ON_GRADIENT.up],
                ["Slow or retrying", counts.warn, "degraded", ON_GRADIENT.warn],
                ["Down", counts.down, "down", ON_GRADIENT.down],
              ] as const
            ).map(([label, n, st], i) => (
              <li key={label} className={`flex h-[30px] items-center gap-2.5 ${i ? "border-t border-white/10" : ""}`}>
                <StatusIcon status={st} className="size-3.5" />
                <span className="flex-1 font-semibold">{label}</span>
                <span className="w-11 text-right font-mono text-white/55">{ms.length ? Math.round((n / ms.length) * 100) : 0}%</span>
                <span className="w-8 text-right font-mono">{n}</span>
              </li>
            ))}
          </ul>
        </GradientCard>

        {/* KPI: time to recover */}
        <GradientCard tone="lime" label="Time to recover" className="col-span-12 min-h-[168px] sm:col-span-6 lg:col-span-3">
          <div className="flex items-center justify-between">
            <div className="text-[11.5px] font-semibold uppercase tracking-[0.06em] text-black/55">Time to recover</div>
            <span className="rounded-full bg-black/85 px-2.5 py-1 font-mono text-[11px] font-semibold text-white">{resolved.length} resolved</span>
          </div>
          <div className="mt-6 text-[44px] leading-none font-extralight tracking-[-0.04em]">{mttr == null ? "—" : fmtDuration(mttr)}</div>
          <div className="mt-3 text-[11.5px] font-medium text-black/60">
            Mean, last {words} · {openNow.length ? `${openNow.length} open now` : "nothing open"}
          </div>
        </GradientCard>

        {/* KPI: certificate runway */}
        <section aria-label="Certificates" className="enter col-span-12 min-h-[168px] rounded-[28px] border border-line bg-surface p-5 sm:col-span-6 sm:p-6 lg:col-span-3">
          <div className="flex items-center justify-between">
            <div className="text-[11.5px] font-semibold uppercase tracking-[0.06em] text-fg-2">Certificates</div>
            <span className="text-xs text-fg-3">Runway</span>
          </div>
          {soonest ? (
            <>
              <div className="mt-3.5 flex items-baseline justify-between gap-2">
                <span className={`tnum text-[38px] leading-none font-extralight tracking-[-0.04em] ${soonest.days <= 14 ? "text-warn" : ""}`}>{soonest.days}</span>
                <span className="truncate text-xs text-fg-2">days · {soonest.name}</span>
              </div>
              <div className="mt-4">
                <TickStrip fraction={Math.min(1, soonest.days / 90)} color={soonest.days <= 7 ? "var(--down)" : soonest.days <= 14 ? "var(--warn)" : "var(--acc)"} off="var(--surface-3)" />
                <div className="mt-1 flex justify-between font-mono text-[10px] text-fg-3">
                  <span>0</span>
                  <span>90 days</span>
                </div>
              </div>
            </>
          ) : (
            <p className="mt-4 text-sm text-fg-2">No HTTPS or TLS monitors yet.</p>
          )}
        </section>

        {/* KPI: next expiries */}
        <section aria-label="Next expiries" className="enter col-span-12 min-h-[168px] rounded-[28px] border border-line bg-surface p-5 sm:col-span-6 sm:p-6 lg:col-span-3">
          <div className="flex items-center justify-between">
            <div className="text-[11.5px] font-semibold uppercase tracking-[0.06em] text-fg-2">Next expiries</div>
            <span className="font-mono text-xs text-fg-3">{certs.length}</span>
          </div>
          <ul className="mt-2.5">
            {certs.slice(0, 3).map((c, i) => (
              <li key={c.id} className={`flex h-[34px] items-center gap-2.5 ${i ? "border-t border-line" : ""}`}>
                <i className="grid size-6 shrink-0 place-items-center rounded-lg bg-surface-3 text-[11px] font-semibold not-italic">{c.name.charAt(0).toUpperCase()}</i>
                <Link href={`/monitors/${c.id}`} className="flex-1 truncate text-[13px] font-semibold hover:underline">
                  {c.name}
                </Link>
                <span className="font-mono text-[11.5px] text-fg-3">{new Date(c.expires).toLocaleDateString([], { month: "short", day: "numeric" })}</span>
                <span className={`w-12 text-right font-mono text-[13px] ${c.days <= 14 ? "text-warn" : ""}`}>{c.days}d</span>
              </li>
            ))}
            {certs.length === 0 && <li className="mt-2 text-sm text-fg-2">Nothing to renew.</li>}
          </ul>
        </section>

        {/* KPI: fleet load */}
        <section aria-label="Fleet load" className="enter col-span-12 min-h-[168px] rounded-[28px] border border-line bg-surface p-5 sm:col-span-6 sm:p-6 lg:col-span-3">
          <div className="flex items-center justify-between">
            <div className="text-[11.5px] font-semibold uppercase tracking-[0.06em] text-fg-2">Fleet load</div>
            <Link href="/servers" className="text-xs text-fg-3 hover:text-fg">
              {online.length}/{ss.filter((s) => s.status !== "new").length} online
            </Link>
          </div>
          {online.length ? (
            <div className="mt-3 flex flex-col gap-3">
              {(
                [
                  ["Avg CPU", avg((s) => s.metrics!.cpu)],
                  ["Avg memory", avg((s) => memPct(s.metrics!))],
                  ["Fullest disk", Math.max(...online.map((s) => s.metrics!.diskPct))],
                ] as const
              ).map(([label, v]) => (
                <div key={label}>
                  <div className="flex justify-between text-[13px] font-semibold">
                    <span>{label}</span>
                    <span className="font-mono font-medium">{fmtPct(v)}</span>
                  </div>
                  <div className="mt-1.5">
                    <TickStrip fraction={(v ?? 0) / 100} n={40} height={10} color={(v ?? 0) >= 85 ? "var(--warn)" : "var(--acc)"} off="var(--surface-3)" />
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="mt-4 text-sm text-fg-2">
              <Link href="/servers" className="text-series hover:underline">
                Connect a server
              </Link>{" "}
              to see CPU, memory and disk here.
            </p>
          )}
        </section>

        {/* network waves */}
        <GradientCard tone="pink" label="Network" className="col-span-12 min-h-[330px] lg:col-span-5">
          <div className="flex items-start justify-between gap-3">
            <div>
              <div className="text-lg font-semibold tracking-[-0.02em]">Network</div>
              <div className="mt-1 text-[12.5px] text-white/60">All servers, last {words}</div>
            </div>
            <div className="flex flex-col items-end gap-1 text-xs font-medium text-white/75">
              <span className="inline-flex items-center gap-1.5">
                <i className="size-2 rounded-full bg-white" />
                In ↑
              </span>
              <span className="inline-flex items-center gap-1.5">
                <i className="size-2 rounded-full bg-[#FFD0E6]" />
                Out ↓
              </span>
            </div>
          </div>
          <div className="mt-3">
            {o.network.length > 1 ? (
              <Waves points={o.network.map((n) => ({ ts: n.ts, a: n.rx, b: n.tx, label: bucketLabel(n.ts, range) }))} fmt={fmtRate} labels={["In", "Out"]} />
            ) : (
              <p className="py-16 text-center text-sm text-white/70">{ss.some((s) => s.status !== "new") ? `Not enough traffic history for ${words} yet.` : "Connect a server to see its traffic here."}</p>
            )}
          </div>
        </GradientCard>

        {/* servers */}
        <GradientCard tone="copper" label="Servers" className="col-span-12 min-h-[330px] sm:col-span-6 lg:col-span-4">
          <div className="flex items-center justify-between">
            <div className="text-lg font-semibold tracking-[-0.02em]">Servers</div>
            <span className="text-[12.5px] text-white/60">memory in use</span>
          </div>
          <div className="mt-3.5 flex items-end justify-between">
            <span className="tnum text-[40px] leading-none font-extralight tracking-[-0.04em]">{online.length ? fmtBytes(memUsed) : "—"}</span>
            {online.length > 0 && <span className="pb-1 text-[12.5px] font-medium text-white/60">of {fmtBytes(memTotal)}</span>}
          </div>
          {online.length > 0 && (
            <>
              <div className="mt-4 flex h-1.5 gap-[3px]">
                {online.map((s, i) => (
                  <div key={s.id} className="rounded-full" style={{ flex: `${s.metrics!.memUsed} 1 0`, background: `rgba(255,255,255,${[0.95, 0.6, 0.32, 0.2][Math.min(i, 3)]})` }} title={`${s.name}: ${fmtBytes(s.metrics!.memUsed)}`} />
                ))}
              </div>
              <ul className="mt-4">
                {online.slice(0, 4).map((s, i) => (
                  <li key={s.id} className={`flex h-[46px] items-center gap-3 ${i ? "border-t border-white/10" : ""}`}>
                    <Link href={`/servers/${s.id}`} className="min-w-0 flex-1">
                      <div className="truncate text-[13.5px] font-semibold">{s.name}</div>
                      <div className="font-mono text-[11px] text-white/55">{fmtBytes(s.metrics!.memUsed)} RAM</div>
                    </Link>
                    <MiniSpark values={s.cpuSpark ?? []} />
                    <div className="w-14 text-right">
                      <div className="font-mono text-[13px]">{fmtPct(s.metrics!.cpu)}</div>
                      <div className="font-mono text-[10.5px] text-white/55">CPU</div>
                    </div>
                  </li>
                ))}
              </ul>
            </>
          )}
          {online.length === 0 && (
            <p className="mt-6 text-sm text-white/75">
              No servers reporting.{" "}
              <Link href="/servers" className="underline">
                Add one
              </Link>
              .
            </p>
          )}
        </GradientCard>

        {/* incidents capsules */}
        <GradientCard tone="green" label="Incidents" className="col-span-12 min-h-[330px] sm:col-span-6 lg:col-span-3">
          <div className="text-lg font-semibold tracking-[-0.02em]">Incidents</div>
          <div className="mt-3.5 tnum text-[34px] leading-none font-extralight tracking-[-0.04em]">{inRange.length}</div>
          <div className="mt-1.5 flex gap-2 text-[12.5px]">
            <span className="font-mono font-semibold" style={{ color: openNow.length ? "#FFD0E6" : "#E8FF1F" }}>
              {openNow.length ? `${openNow.length} open` : "all resolved"}
            </span>
            <span className="text-white/60">last {words}</span>
          </div>
          <div className="mt-5">
            <Capsules items={caps} fmtTip={(v) => String(v)} />
          </div>
        </GradientCard>

        {/* treemap */}
        <section aria-label="Memory by process" className="enter col-span-12 rounded-[28px] border border-line bg-surface p-5 sm:p-6 lg:col-span-7">
          <div className="flex flex-col items-start justify-between gap-3 sm:flex-row sm:items-center">
            <h2 className="text-[17px] font-semibold tracking-[-0.02em]">Memory by process</h2>
            <div className="flex flex-wrap gap-x-3.5 gap-y-1.5 text-xs text-fg-2">
              {groups.map((g) => (
                <span key={g.id} className="inline-flex items-center gap-1.5">
                  <i className="size-2 rounded-[2px]" style={{ background: g.color }} />
                  {g.name}
                </span>
              ))}
            </div>
          </div>
          <p className="mt-1 text-xs text-fg-3">Top processes by resident memory on each online server, from the latest snapshot.</p>
          <div className="mt-4">
            {treeItems.length ? <Treemap items={treeItems} groups={groups} fmt={(v) => fmtBytes(v)} height={300} /> : <p className="grid h-40 place-items-center text-sm text-fg-2">No server snapshots yet.</p>}
          </div>
        </section>

        {/* radar */}
        <section aria-label="When incidents start" className="enter col-span-12 rounded-[28px] border border-line bg-surface p-5 sm:p-6 lg:col-span-5">
          <h2 className="text-[17px] font-semibold tracking-[-0.02em]">When incidents start</h2>
          <p className="mt-1 text-xs text-fg-3">By hour of day, your time, last {words}.</p>
          <Radar
            values={byHour.map((h) => h.total)}
            axis={byHour.map((_, i) => String(i).padStart(2, "0"))}
            color="var(--series)"
            label={`Incidents by hour of day; busiest ${String(busiest).padStart(2, "0")}:00`}
            fmt={(v, i) => [
              { label: "Incidents", value: String(v), color: "var(--series)" },
              { label: "Monitors", value: String(byHour[i].monitor) },
              { label: "Servers", value: String(byHour[i].server) },
            ]}
          />
          <div className="mt-2 grid grid-cols-2 gap-3">
            <div>
              <div className="text-[13px] text-fg-2">Busiest hour</div>
              <div className="tnum mt-1 font-mono text-[26px] leading-none">{inRange.length ? `${String(busiest).padStart(2, "0")}:00` : "—"}</div>
              <div className="mt-1 text-xs text-fg-3">{inRange.length ? `${byHour[busiest].total} incidents` : "no incidents"}</div>
            </div>
            <div>
              <div className="text-[13px] text-fg-2">Split</div>
              <div className="tnum mt-1 font-mono text-[26px] leading-none">
                {inRange.filter((f) => f.source === "monitor").length}
                <span className="text-fg-3"> / </span>
                {inRange.filter((f) => f.source === "server").length}
              </div>
              <div className="mt-1 text-xs text-fg-3">monitors / servers</div>
            </div>
          </div>
        </section>

        {/* recent incidents */}
        <section aria-label="Recent incidents" className="enter col-span-12 rounded-[28px] border border-line bg-surface p-5 sm:p-6 lg:col-span-4">
          <h2 className="mb-3 text-[17px] font-semibold tracking-[-0.02em]">Recent incidents</h2>
          {o.feed.length === 0 ? (
            <p className="text-sm text-fg-2">Nothing in the last {words}.</p>
          ) : (
            <ul className="-my-1">
              {o.feed.slice(0, 7).map((f, i) => (
                <li key={i} className="flex items-start gap-3 border-t border-line py-2.5 first:border-t-0">
                  <StatusIcon status={f.resolvedAt == null ? (f.kind === "down" || f.kind === "offline" ? "down" : "degraded") : "up"} className="mt-0.5 size-4" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-baseline justify-between gap-2">
                      <Link href={`/${f.source === "server" ? "servers" : "monitors"}/${f.id}`} className="truncate text-sm font-medium hover:underline">
                        {f.name}
                      </Link>
                      <span className="shrink-0 text-xs text-fg-3">{fmtDateTime(f.startedAt)}</span>
                    </div>
                    <div className="truncate text-xs text-fg-2">
                      {KIND_LABEL[f.kind] ?? f.kind} · {f.resolvedAt == null ? `ongoing ${fmtDuration(now - f.startedAt)}` : `lasted ${fmtDuration(f.resolvedAt - f.startedAt)}`}
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>

        {/* monitor grid */}
        <section aria-label="All monitors" className="enter col-span-12 rounded-[28px] border border-line bg-surface p-5 sm:p-6 lg:col-span-8">
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-[17px] font-semibold tracking-[-0.02em]">All monitors</h2>
            <Link href="/monitors" className="text-xs text-fg-3 hover:text-fg">
              Manage →
            </Link>
          </div>
          {ms.length === 0 ? (
            <p className="text-sm text-fg-2">
              No monitors yet.{" "}
              <Link href="/monitors/new" className="text-series hover:underline">
                Add one
              </Link>
              .
            </p>
          ) : (
            <div className="grid gap-2 sm:grid-cols-2">
              {ms.map((m) => {
                const st = shownStatus(m);
                return (
                <Link key={m.id} href={`/monitors/${m.id}`} className="rounded-2xl border border-line p-3 transition-colors hover:bg-surface-2">
                  <div className="flex items-center gap-2">
                    <StatusIcon status={st === "new" ? "pending" : st} className="size-3.5" />
                    <span className="flex-1 truncate text-[13px] font-semibold">{m.name}</span>
                    <span className="font-mono text-xs text-fg-2">{fmtUptime(m.uptime24h)}</span>
                  </div>
                  <HeartbeatBar beats={m.beats} height={16} className="mt-2" />
                </Link>
                );
              })}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

/** White sparkline for gradient cards (finance crypto rows). */
function MiniSpark({ values }: { values: number[] }) {
  const W = 84, H = 28;
  if (values.length < 2) return <span className="w-[84px]" />;
  const hi = Math.max(1, ...values), lo = Math.min(...values), pad = (hi - lo) * 0.15 || 1;
  const Y = (v: number) => H - 3 - ((v - lo + pad) / (hi - lo + 2 * pad)) * (H - 6);
  const P = values.map((v, i) => [2 + (i / (values.length - 1)) * (W - 6), Y(v)] as [number, number]);
  const e = P[P.length - 1];
  const d = P.map((p, i) => (i ? "L" : "M") + p[0].toFixed(1) + "," + p[1].toFixed(1)).join("");
  return (
    <svg width={W} height={H} aria-hidden className="shrink-0">
      <path d={`${d}L${e[0]},${H}L${P[0][0]},${H}Z`} fill="rgba(255,255,255,.14)" />
      <path d={d} fill="none" stroke="#fff" strokeWidth={1.5} strokeLinejoin="round" />
      <circle cx={e[0]} cy={e[1]} r={2.6} fill="#E8FF1F" stroke="rgba(0,0,0,.35)" />
    </svg>
  );
}
