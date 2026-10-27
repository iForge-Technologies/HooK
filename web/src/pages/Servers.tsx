import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "wouter";
import { Sparkline, TickGauge } from "../components/server-charts";
import { serverBadge, StatusBadge, StatusIcon } from "../components/status";
import { Button, Card, Empty, Spinner } from "../components/ui";
import { api, type Server } from "../lib/api";
import { fmtAgo, fmtBytes, fmtPct, fmtRate, fmtUptimeSecs, memPct } from "../lib/format";
import { useNow } from "../lib/hooks";
import { AddServerModal } from "./ServerModals";

const ALERT_LABEL: Record<string, string> = { offline: "Offline", cpu: "High CPU", mem: "High memory", disk: "Disk filling", container: "Container problem", service: "Service down", forecast: "Disk filling soon" };

export function ServersPage() {
  const q = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers"), refetchInterval: 60_000 });
  const [adding, setAdding] = useState(false);
  const now = useNow(5000);
  const list = q.data ?? [];

  const stats = useMemo(() => {
    const live = list.filter((s) => s.status !== "new");
    const online = live.filter((s) => s.status === "online");
    const alerts = list.reduce((n, s) => n + s.openAlerts.length, 0);
    const busiest = [...online].filter((s) => s.metrics).sort((a, b) => b.metrics!.cpu - a.metrics!.cpu)[0];
    const fullest = [...online].filter((s) => s.metrics).sort((a, b) => b.metrics!.diskPct - a.metrics!.diskPct)[0];
    return { total: live.length, online: online.length, alerts, busiest, fullest };
  }, [list]);

  const groups = useMemo(() => {
    const map = new Map<string, Server[]>();
    for (const s of list) map.set(s.group, [...(map.get(s.group) ?? []), s]);
    return [...map.entries()].sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)));
  }, [list]);

  if (q.isPending) {
    return (
      <div className="grid h-64 place-items-center text-fg-2">
        <Spinner className="size-5" />
      </div>
    );
  }

  const offline = stats.total - stats.online;
  const tone = stats.total === 0 ? "hero-idle" : offline > 0 ? "hero-bad" : "hero-ok";

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-[28px] font-bold tracking-[-0.03em]">Servers</h1>
          <p className="mt-0.5 text-sm text-fg-2">Live resource usage from the Hook agent on each VPS.</p>
        </div>
        <Button variant="primary" onClick={() => setAdding(true)}>
          <svg viewBox="0 0 16 16" className="size-3.5" aria-hidden>
            <path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </svg>
          Add server
        </Button>
      </div>

      {list.length === 0 ? (
        <Card>
          <Empty title="Connect your first server" action={<Button variant="primary" onClick={() => setAdding(true)}>Add server</Button>}>
            Run one command on a VPS to install the agent. You'll see CPU, memory, disks, network and processes here within seconds.
          </Empty>
        </Card>
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-12">
            <section className={`hero-card enter col-span-2 flex min-h-[150px] flex-col justify-between rounded-[24px] p-5 sm:p-6 lg:col-span-6 ${tone}`} aria-label="Fleet status">
              <div className="text-[15px] font-semibold tracking-[-0.02em]">
                {stats.total === 0 ? "Waiting for agents" : offline > 0 ? `${offline} ${offline === 1 ? "server is" : "servers are"} offline` : "All servers reporting"}
              </div>
              <div>
                <div className="tnum text-[52px] leading-none font-extralight tracking-[-0.05em]">
                  {stats.online}
                  <span className="text-[26px] text-white/55">/{stats.total}</span>
                </div>
                <div className="mt-2 text-xs font-medium text-white/70">servers online</div>
              </div>
            </section>
            <Tile label="Active alerts" value={String(stats.alerts)} note={stats.alerts ? "Thresholds or offline" : "All within limits"} tone={stats.alerts ? "warn" : undefined} />
            <Tile label="Busiest CPU" value={stats.busiest ? fmtPct(stats.busiest.metrics!.cpu) : "—"} note={stats.busiest?.name ?? "No data yet"} />
            <Tile label="Fullest disk" value={stats.fullest ? fmtPct(stats.fullest.metrics!.diskPct) : "—"} note={stats.fullest?.name ?? "No data yet"} />
          </div>

          {groups.map(([group, servers]) => (
            <section key={group || "_"} aria-label={group || "Ungrouped"}>
              {(groups.length > 1 || group) && <h2 className="mb-3 text-[13px] font-semibold uppercase tracking-[0.05em] text-fg-2">{group || "Ungrouped"}</h2>}
              <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
                {servers.map((s) => (
                  <ServerCard key={s.id} s={s} now={now} />
                ))}
              </div>
            </section>
          ))}
        </>
      )}
      <AddServerModal open={adding} onClose={() => setAdding(false)} />
    </div>
  );
}

function Tile({ label, value, note, tone }: { label: string; value: string; note: string; tone?: "warn" }) {
  return (
    <section className="enter flex min-h-[120px] flex-col justify-between rounded-[24px] border border-line bg-surface p-4 sm:p-5 lg:col-span-2">
      <div className="flex items-center gap-2 text-[11.5px] font-semibold uppercase tracking-[0.06em] text-fg-2">
        {tone && <span className="size-1.5 rounded-full bg-warn" aria-hidden />}
        {label}
      </div>
      <div className="min-w-0">
        <div className="tnum text-[32px] leading-none font-light tracking-[-0.04em]">{value}</div>
        <div className="mt-1.5 truncate text-xs text-fg-3">{note}</div>
      </div>
    </section>
  );
}

function ServerCard({ s, now }: { s: Server; now: number }) {
  const m = s.metrics;
  const b = serverBadge(s.status);
  const stale = s.status !== "online";
  const th = s.thresholds;
  return (
    <Link href={`/servers/${s.id}`} className="enter group block rounded-[24px] border border-line bg-surface p-5 transition-colors hover:border-line-strong hover:bg-surface-2/40">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-[15px] font-semibold tracking-[-0.02em]">{s.name}</div>
          <div className="mt-0.5 truncate text-xs text-fg-3">{s.status === "new" ? "Agent not installed yet" : [s.info.hostname, s.info.platform].filter(Boolean).join(" · ")}</div>
        </div>
        <StatusBadge status={b.status} label={b.label} pulse />
      </div>

      {s.status === "new" ? (
        <p className="mt-5 text-sm text-fg-2">Open the server to get its install command.</p>
      ) : (
        <>
          <div className={`mt-5 grid grid-cols-3 gap-4 ${stale ? "opacity-50" : ""}`}>
            <TickGauge label="CPU" value={m?.cpu ?? null} limit={th.cpu} display={fmtPct(m?.cpu)} ticks={16} />
            <TickGauge label="Memory" value={m ? memPct(m) : null} limit={th.mem} display={m ? fmtPct(memPct(m)) : "—"} ticks={16} />
            <TickGauge label="Disk" value={m?.diskPct ?? null} limit={th.disk} display={fmtPct(m?.diskPct)} ticks={16} />
          </div>
          <dl className={`mt-4 grid grid-cols-3 gap-2 text-xs ${stale ? "opacity-50" : ""}`}>
            <div>
              <dt className="text-fg-3">Load</dt>
              <dd className="tnum font-mono">{m ? m.load1.toFixed(2) : "—"}</dd>
            </div>
            <div>
              <dt className="text-fg-3">Net ↓ / ↑</dt>
              <dd className="tnum truncate font-mono">{m ? `${fmtRate(m.netRx)} · ${fmtRate(m.netTx)}` : "—"}</dd>
            </div>
            <div className="text-right">
              <dt className="text-fg-3">RAM</dt>
              <dd className="tnum font-mono">{fmtBytes(s.info.memTotal, 0)}</dd>
            </div>
          </dl>
          <div className="mt-3">
            <Sparkline values={s.cpuSpark ?? []} max={100} height={34} />
          </div>
          <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-fg-3">
            <span>CPU · last hour</span>
            <span>{s.status === "online" ? `up ${fmtUptimeSecs(Math.max(0, now / 1000 - s.info.bootTime))}` : `last seen ${fmtAgo(s.lastSeen, now)}`}</span>
          </div>
        </>
      )}

      {s.diskDaysToFull != null && s.diskDaysToFull <= 30 && !s.openAlerts.includes("forecast") && (
        <div className="mt-3">
          <span className="inline-flex h-6 items-center gap-1.5 rounded-lg px-2 text-xs font-medium" style={{ background: "color-mix(in oklab, var(--warn) 14%, transparent)" }}>
            <StatusIcon status="degraded" className="size-3" />
            Disk full in ~{Math.max(1, Math.round(s.diskDaysToFull))} days
          </span>
        </div>
      )}
      {s.openAlerts.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-1.5">
          {s.openAlerts.map((k) => (
            <span key={k} className="inline-flex h-6 items-center gap-1.5 rounded-lg px-2 text-xs font-medium" style={{ background: "color-mix(in oklab, var(--warn) 14%, transparent)" }}>
              <StatusIcon status={k === "offline" ? "down" : "degraded"} className="size-3" />
              {ALERT_LABEL[k] ?? k}
            </span>
          ))}
        </div>
      )}
    </Link>
  );
}
