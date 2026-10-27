import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "wouter";
import { CoreColumns, MirrorChart, TickGauge, TimeChart, type SeriesDef } from "../components/server-charts";
import { serverBadge, StatusBadge, StatusIcon } from "../components/status";
import { Button, Card, CardTitle, Chip, Segmented, Spinner } from "../components/ui";
import { api, type HostEvent, type MetricPoint, type Proc, type ServerDetail, type ServerSeries } from "../lib/api";
import { fmtAgo, fmtBytes, fmtDateTime, fmtDuration, fmtPct, fmtRate, fmtUptimeSecs, memPct } from "../lib/format";
import { useNow } from "../lib/hooks";
import { EditServerModal, InstallCommand } from "./ServerModals";
import { DockerCard, ServicesCard } from "../components/server-panels";
import { GPUCard } from "../components/server-gpu";
import { InterfacesCard, NetworkPanels } from "../components/server-network";
import { ForecastCard } from "../components/forecast-card";

type Range = "1h" | "6h" | "24h" | "7d" | "30d";

const pct = (v: number) => `${Math.round(v)}%`;
const pctMem = (p: MetricPoint) => (p.memTotal ? (p.memUsed / p.memTotal) * 100 : 0);
const pctSwap = (p: MetricPoint) => (p.swapTotal ? (p.swapUsed / p.swapTotal) * 100 : 0);

// Categorical slots in the validated order: blue, orange, aqua.
const C1 = "var(--cat-1)", C2 = "var(--cat-2)", C3 = "var(--cat-3)";

const CPU: SeriesDef[] = [
  { label: "Used", color: C1, get: (p) => p.cpu, peak: (p) => p.cpuMax },
  { label: "I/O wait", color: C2, get: (p) => p.iowait },
  { label: "Steal", color: C3, get: (p) => p.steal },
];
const MEM: SeriesDef[] = [
  { label: "Memory", color: C1, get: pctMem, peak: (p) => (p.memUsedMax != null && p.memTotal ? (p.memUsedMax / p.memTotal) * 100 : undefined) },
  { label: "Swap", color: C2, get: pctSwap },
];
const LOAD: SeriesDef[] = [
  { label: "1 min", color: C1, get: (p) => p.load1, peak: (p) => p.load1Max },
  { label: "5 min", color: C2, get: (p) => p.load5 },
  { label: "15 min", color: C3, get: (p) => p.load15 },
];
const DISK: SeriesDef[] = [{ label: "Fullest mount", color: C1, get: (p) => p.diskPct }];
const GPU: SeriesDef[] = [
  { label: "Utilisation", color: C1, get: (p) => p.gpu ?? 0, peak: (p) => p.gpuMax },
  { label: "Memory", color: C2, get: (p) => p.gpuMem ?? 0 },
];
const NET_IN: SeriesDef = { label: "In", color: C1, get: (p) => p.netRx, peak: (p) => p.netRxMax };
const NET_OUT: SeriesDef = { label: "Out", color: C2, get: (p) => p.netTx, peak: (p) => p.netTxMax };
const IO_R: SeriesDef = { label: "Read", color: C1, get: (p) => p.diskRead, peak: (p) => p.diskReadMax };
const IO_W: SeriesDef = { label: "Write", color: C2, get: (p) => p.diskWrite, peak: (p) => p.diskWriteMax };

const EVENT_LABEL: Record<HostEvent["kind"], string> = {
  offline: "Stopped reporting",
  cpu: "High CPU",
  mem: "High memory",
  disk: "Disk filling up",
  container: "Container problem",
  service: "Service down",
  forecast: "Disk filling (forecast)",
};
const eventLabel = (e: HostEvent): string => (e.subject ? `${EVENT_LABEL[e.kind]}: ${e.subject}` : EVENT_LABEL[e.kind]);
const hasPeak = (e: HostEvent) => e.kind === "cpu" || e.kind === "mem" || e.kind === "disk";

export function ServerDetailPage({ id }: { id: number }) {
  const [range, setRange] = useState<Range>("1h");
  const [editing, setEditing] = useState(false);
  const now = useNow(5000);
  const q = useQuery({ queryKey: ["server", id], queryFn: () => api.get<ServerDetail>(`/servers/${id}`), refetchInterval: 60_000 });
  const series = useQuery({
    queryKey: ["serverSeries", id, range],
    queryFn: () => api.get<ServerSeries>(`/servers/${id}/series?range=${range}`),
    placeholderData: (prev) => prev,
    refetchInterval: range === "1h" ? false : 60_000,
  });
  const points = series.data?.points ?? [];
  const hasSwap = useMemo(() => points.some((p) => p.swapTotal > 0), [points]);
  const gpuHistory = useMemo(() => points.some((p) => (p.gpu ?? 0) > 0 || (p.gpuMem ?? 0) > 0), [points]);

  if (q.isPending) {
    return (
      <div className="grid h-64 place-items-center text-fg-2">
        <Spinner className="size-5" />
      </div>
    );
  }
  if (q.isError) {
    return (
      <Card>
        <p className="font-semibold">Couldn't load this server.</p>
        <p className="mt-1 text-sm text-fg-2">{(q.error as Error).message}</p>
        <Link href="/servers" className="mt-4 inline-block text-sm text-series">
          ← Back to servers
        </Link>
      </Card>
    );
  }

  const { server: s, snapshot: snap, events, forecast } = q.data;
  const m = s.metrics;
  const b = serverBadge(s.status);
  const info = s.info;
  const th = s.thresholds;
  const fresh = s.status === "online";
  const hasGPU = !!snap?.gpu?.present;

  return (
    <div className="flex flex-col gap-4 sm:gap-5">
      <Link href="/servers" className="inline-flex w-fit items-center gap-1 text-sm text-fg-2 hover:text-fg">
        <svg viewBox="0 0 16 16" className="size-3.5" aria-hidden>
          <path d="M10 3.5L5.5 8l4.5 4.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        Servers
      </Link>

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="truncate text-[28px] font-bold tracking-[-0.03em]">{s.name}</h1>
            <StatusBadge status={b.status} label={b.label} size="lg" pulse />
          </div>
          <p className="mt-2 text-sm text-fg-3">
            {s.status === "new"
              ? "The agent hasn't connected yet."
              : s.status === "online"
                ? `Reporting every 5s · last frame ${fmtAgo(m?.ts ?? s.lastSeen, now)}`
                : `Offline for ${fmtDuration(now - s.statusSince)} · last seen ${fmtDateTime(s.lastSeen)}`}
          </p>
        </div>
        <Button size="sm" onClick={() => setEditing(true)}>
          Edit & alerts
        </Button>
      </div>

      {s.status === "new" && <NewServerHelp id={id} />}

      {s.status !== "new" && (
        <>
          <div className="flex flex-wrap gap-2">
            {info.hostname && <Chip>{info.hostname}</Chip>}
            {info.platform && <Chip>{info.platform}</Chip>}
            {info.kernel && <Chip>kernel {info.kernel}</Chip>}
            <Chip>
              {info.cores} × {info.arch}
            </Chip>
            {info.cpuModel && <Chip className="max-w-full truncate">{info.cpuModel}</Chip>}
            <Chip>{fmtBytes(info.memTotal, 0)} RAM</Chip>
            {info.virtualization && <Chip>{info.virtualization}</Chip>}
            {s.remoteIp && <Chip>{s.remoteIp}</Chip>}
            {snap && <Chip>up {fmtUptimeSecs(snap.uptime)}</Chip>}
            <Chip>agent {info.agentVersion}</Chip>
          </div>

          {(s.openAlerts.length > 0 || snap?.rebootRequired) && (
            <div className="flex flex-col gap-2">
              {events
                .filter((e) => e.resolvedAt == null)
                .map((e) => (
                  <div key={e.id} role="alert" className="flex items-center gap-3 rounded-2xl border border-warn/40 bg-warn/10 px-4 py-3 text-sm">
                    <StatusIcon status={e.kind === "offline" ? "down" : "degraded"} className="size-4" />
                    <span className="font-medium">{eventLabel(e)}</span>
                    <span className="text-fg-2">
                      since {fmtDateTime(e.startedAt)}
                      {hasPeak(e) && ` · peak ${Math.round(e.peak)}%`}
                    </span>
                  </div>
                ))}
              {snap?.rebootRequired && (
                <div className="flex items-center gap-3 rounded-2xl border border-line-strong bg-surface-2 px-4 py-3 text-sm">
                  <StatusIcon status="degraded" className="size-4" />
                  <span>
                    <b>Reboot required</b> <span className="text-fg-2">to finish installing updates.</span>
                  </span>
                </div>
              )}
            </div>
          )}

          {/* live tiles */}
          <div className={`grid grid-cols-2 gap-3 sm:grid-cols-3 ${hasGPU ? "lg:grid-cols-7" : "lg:grid-cols-6"} ${fresh ? "" : "opacity-60"}`}>
            <GaugeTile label="CPU" value={m?.cpu ?? null} limit={th.cpu} display={fmtPct(m?.cpu)} sub={m ? `iowait ${fmtPct(m.iowait)} · steal ${fmtPct(m.steal)}` : ""} />
            <GaugeTile label="Memory" value={m ? memPct(m) : null} limit={th.mem} display={m ? fmtPct(memPct(m)) : "—"} sub={m ? `${fmtBytes(m.memUsed)} of ${fmtBytes(m.memTotal)}` : ""} />
            <GaugeTile label="Disk" value={m?.diskPct ?? null} limit={th.disk} display={fmtPct(m?.diskPct)} sub="fullest mount" />
            <Stat label="Load" value={m ? m.load1.toFixed(2) : "—"} sub={m ? `${m.load5.toFixed(2)} · ${m.load15.toFixed(2)} · ${info.cores} cores` : ""} />
            <Stat label="Network" value={m ? `↓ ${fmtRate(m.netRx)}` : "—"} sub={m ? `↑ ${fmtRate(m.netTx)}` : ""} />
            <Stat label="TCP · procs" value={m ? `${Math.round(m.tcp)}` : "—"} sub={m ? `${Math.round(m.procs)} processes` : ""} />
            {hasGPU && <GaugeTile label="GPU" value={m?.gpu ?? null} limit={0} display={fmtPct(m?.gpu)} sub={m ? `memory ${fmtPct(m.gpuMem)}` : ""} />}
          </div>

          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-[15px] font-semibold tracking-[-0.02em]">History</h2>
            <Segmented label="Time range" value={range} onChange={setRange} items={(["1h", "6h", "24h", "7d", "30d"] as const).map((r) => ({ value: r, label: r }))} />
          </div>
          {points.length < 2 ? (
            <Card>
              <div className="grid h-40 place-items-center text-sm text-fg-2">{series.isPending ? <Spinner /> : "Not enough data for this range yet."}</div>
            </Card>
          ) : (
            <div className="grid gap-4 sm:gap-5 lg:grid-cols-2">
              <ChartCard title="CPU">
                <TimeChart points={points} series={CPU} fmt={pct} yMax={100} reference={th.cpu > 0 ? { value: th.cpu, label: `alert at ${th.cpu}%` } : undefined} />
              </ChartCard>
              <ChartCard title="Memory">
                <TimeChart points={points} series={hasSwap ? MEM : MEM.slice(0, 1)} fmt={pct} yMax={100} reference={th.mem > 0 ? { value: th.mem, label: `alert at ${th.mem}%` } : undefined} />
              </ChartCard>
              <ChartCard title="Load average">
                <TimeChart points={points} series={LOAD} fmt={(v) => v.toFixed(v >= 10 ? 0 : 1)} reference={{ value: info.cores, label: `${info.cores} cores` }} />
              </ChartCard>
              <ChartCard title="Disk usage">
                <TimeChart points={points} series={DISK} fmt={pct} yMax={100} reference={th.disk > 0 ? { value: th.disk, label: `alert at ${th.disk}%` } : undefined} />
              </ChartCard>
              <ChartCard title="Network">
                <MirrorChart points={points} up={NET_IN} down={NET_OUT} fmt={fmtRate} />
              </ChartCard>
              <ChartCard title="Disk I/O">
                <MirrorChart points={points} up={IO_R} down={IO_W} fmt={fmtRate} />
              </ChartCard>
              {(hasGPU || gpuHistory) && (
                <ChartCard title="GPU">
                  <TimeChart points={points} series={GPU} fmt={pct} yMax={100} />
                </ChartCard>
              )}
            </div>
          )}
          {series.data && series.data.resolution !== "raw" && (
            <p className="-mt-2 text-xs text-fg-3">{series.data.resolution === "1m" ? "Per-minute averages" : "Hourly averages"}; hover a point to see the peak inside it.</p>
          )}

          {forecast && <ForecastCard forecast={forecast} />}

          <h2 className="-mb-1 text-[15px] font-semibold tracking-[-0.02em]">Network</h2>
          <div className="grid gap-4 sm:gap-5 lg:grid-cols-2">
            <NetworkPanels serverId={id} online={s.status === "online" && s.connected} speedTestOff={!!info.noSpeedTest} />
            {snap && <InterfacesCard ifaces={snap.ifaces ?? []} />}
          </div>

          {snap && (
            <div className="grid gap-4 sm:gap-5 lg:grid-cols-2">
              {snap.cores && snap.cores.length > 0 && (
                <Card>
                  <CardTitle aside={<span className="text-xs text-fg-3">live · every 30s</span>}>Per-core CPU</CardTitle>
                  <CoreColumns cores={snap.cores} />
                  <div className="mt-2 flex justify-between text-[11px] text-fg-3">
                    <span>core 0</span>
                    <span>core {snap.cores.length - 1}</span>
                  </div>
                </Card>
              )}
              <Card>
                <CardTitle>Mounts</CardTitle>
                <ul className="flex flex-col gap-4">
                  {(snap.mounts ?? []).map((mt) => (
                    <li key={mt.path}>
                      <div className="mb-1 flex items-baseline justify-between gap-3 text-sm">
                        <span className="truncate font-mono text-[13px]">{mt.path}</span>
                        <span className="shrink-0 text-xs text-fg-2">
                          {fmtBytes(mt.used)} of {fmtBytes(mt.total)} · {mt.fstype}
                          {mt.inodesPct > 0 && ` · inodes ${fmtPct(mt.inodesPct)}`}
                        </span>
                      </div>
                      <TickGauge label="" value={mt.usedPct} limit={th.disk} display="" ticks={40} />
                      <div className="mt-0.5 text-right font-mono text-xs">{fmtPct(mt.usedPct, 1)}</div>
                    </li>
                  ))}
                </ul>
              </Card>
              <ProcTable title="Top processes by CPU" procs={snap.topCpu ?? []} by="cpu" />
              <ProcTable title="Top processes by memory" procs={snap.topMem ?? []} by="mem" />
              <Card>
                <CardTitle>Alert history</CardTitle>
                {events.length === 0 ? (
                  <p className="text-sm text-fg-2">No alerts yet.</p>
                ) : (
                  <ul className="-my-1">
                    {events.map((e) => (
                      <li key={e.id} className="flex items-start gap-3 border-t border-line py-3 first:border-t-0">
                        <StatusIcon status={e.resolvedAt == null ? (e.kind === "offline" ? "down" : "degraded") : "up"} className="mt-0.5 size-4" />
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                            <span className="text-sm font-medium">
                              {eventLabel(e)}
                              {hasPeak(e) && <span className="font-normal text-fg-2"> · peak {Math.round(e.peak)}%</span>}
                            </span>
                            <span className="text-xs text-fg-3">{fmtDateTime(e.startedAt)}</span>
                          </div>
                          <p className="mt-0.5 text-xs text-fg-2">
                            {e.resolvedAt == null ? "Ongoing" : `Lasted ${fmtDuration(e.resolvedAt - e.startedAt)}`}
                            {e.detail && ` · ${e.detail}`}
                          </p>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
              </Card>
              <GPUCard gpu={snap.gpu} />
              {snap.docker && <DockerCard docker={snap.docker} />}
              <ServicesCard units={snap.units ?? []} failed={snap.failedUnits ?? []} watched={s.thresholds.units ?? []} />
              {snap.temps && snap.temps.length > 0 && (
                <Card>
                  <CardTitle>Temperatures</CardTitle>
                  <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                    {snap.temps.map((t) => (
                      <div key={t.sensor}>
                        <dt className="truncate text-xs text-fg-3">{t.sensor}</dt>
                        <dd className={`tnum font-mono text-lg ${t.c >= 85 ? "text-warn" : ""}`}>{t.c.toFixed(0)}°C</dd>
                      </div>
                    ))}
                  </dl>
                </Card>
              )}
            </div>
          )}
        </>
      )}
      <EditServerModal server={s} open={editing} onClose={() => setEditing(false)} />
    </div>
  );
}

function ChartCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardTitle>{title}</CardTitle>
      {children}
    </Card>
  );
}

function GaugeTile({ label, value, limit, display, sub }: { label: string; value: number | null; limit: number; display: string; sub: string }) {
  return (
    <div className="rounded-[20px] border border-line bg-surface p-4">
      <TickGauge label={label} value={value} limit={limit} display={display} ticks={20} />
      <div className="mt-2 truncate text-xs text-fg-3">{sub}</div>
    </div>
  );
}

function Stat({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="rounded-[20px] border border-line bg-surface p-4">
      <div className="text-xs font-medium text-fg-2">{label}</div>
      <div className="tnum mt-1.5 truncate font-mono text-lg tracking-[-0.03em]">{value}</div>
      <div className="mt-0.5 truncate text-xs text-fg-3">{sub}</div>
    </div>
  );
}

function ProcTable({ title, procs, by }: { title: string; procs: Proc[]; by: "cpu" | "mem" }) {
  return (
    <Card>
      <CardTitle aside={<span className="text-xs text-fg-3">names only, no arguments</span>}>{title}</CardTitle>
      {procs.length === 0 ? (
        <p className="text-sm text-fg-2">Nothing busy right now.</p>
      ) : (
        <div className="-mx-1 overflow-x-auto">
          <table className="w-full min-w-[340px] text-sm">
            <thead>
              <tr className="text-left text-xs text-fg-3">
                <th className="px-1 pb-2 font-medium">Process</th>
                <th className="px-1 pb-2 font-medium">User</th>
                <th className="px-1 pb-2 text-right font-medium">CPU</th>
                <th className="px-1 pb-2 text-right font-medium">Memory</th>
              </tr>
            </thead>
            <tbody>
              {procs.map((p) => (
                <tr key={p.pid} className="border-t border-line">
                  <td className="max-w-[160px] truncate px-1 py-2 font-mono text-[13px]" title={`PID ${p.pid}`}>
                    {p.name || `pid ${p.pid}`}
                  </td>
                  <td className="max-w-[100px] truncate px-1 py-2 text-xs text-fg-2">{p.user || "—"}</td>
                  <td className={`tnum px-1 py-2 text-right font-mono text-xs ${by === "cpu" ? "text-fg" : "text-fg-2"}`}>{p.cpu.toFixed(1)}%</td>
                  <td className={`tnum px-1 py-2 text-right font-mono text-xs ${by === "mem" ? "text-fg" : "text-fg-2"}`}>{fmtBytes(p.rss)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

/** Lets you fetch a fresh install command for a server that never connected. */
function NewServerHelp({ id }: { id: number }) {
  const [install, setInstall] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const get = async () => {
    setBusy(true);
    try {
      setInstall((await api.post<{ install: string }>(`/servers/${id}/token`)).install);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card>
      <CardTitle>Install the agent</CardTitle>
      {install ? (
        <InstallCommand command={install} />
      ) : (
        <div className="flex flex-wrap items-center gap-3">
          <p className="text-sm text-fg-2">Tokens are only shown once. Create a new install command to connect this server.</p>
          <Button size="sm" variant="primary" loading={busy} onClick={get}>
            Get install command
          </Button>
        </div>
      )}
    </Card>
  );
}
