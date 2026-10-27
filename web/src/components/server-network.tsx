import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { api, type Iface, type ServerNetwork, type SpeedTest } from "../lib/api";
import { fmtAgo, fmtBytes, fmtRate } from "../lib/format";
import { useSize } from "../lib/hooks";
import { useTip } from "./overlay";
import { Button, Card, CardTitle, Chip, ErrorNote, Spinner } from "./ui";

const IN = "var(--cat-1)", OUT = "var(--cat-2)";
const DAY = 86_400_000;

export function fmtBits(bps: number | null | undefined): string {
  if (bps == null || bps <= 0) return "—";
  if (bps >= 1e9) return `${(bps / 1e9).toFixed(2)} Gbit/s`;
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(bps >= 1e8 ? 0 : 1)} Mbit/s`;
  return `${Math.round(bps / 1e3)} kbit/s`;
}

const fmtDay = (ms: number) => new Date(ms).toLocaleDateString([], { month: "short", day: "numeric", timeZone: "UTC" });

/** Traffic totals and speed tests for one server. */
export function NetworkPanels({ serverId, online, speedTestOff }: { serverId: number; online: boolean; speedTestOff: boolean }) {
  const q = useQuery({ queryKey: ["serverNetwork", serverId], queryFn: () => api.get<ServerNetwork>(`/servers/${serverId}/network`), refetchInterval: 60_000 });
  if (!q.data) {
    return (
      <Card className="lg:col-span-2">
        <CardTitle>Traffic</CardTitle>
        {q.isError ? <ErrorNote>{(q.error as Error).message}</ErrorNote> : <Spinner />}
      </Card>
    );
  }
  return (
    <>
      <TrafficCard n={q.data} />
      <SpeedTestCard serverId={serverId} n={q.data} online={online} off={speedTestOff || !q.data.speedTestEnabled} />
    </>
  );
}

function TrafficCard({ n }: { n: ServerNetwork }) {
  const { cycle } = n;
  const total = cycle.rx + cycle.tx;
  const quota = cycle.quotaGb * 1e9;
  const now = Date.now();
  // Tracking may have started mid-cycle (new server or agent update): project
  // from the first day with data, and only once there are two days of it.
  const first = n.days.find((d) => d.day >= cycle.start && d.rx + d.tx > 0)?.day;
  const partial = first != null && first > cycle.start;
  const elapsed = first == null ? 0 : (now - first) / DAY;
  const remaining = (cycle.end - now) / DAY;
  const projected = elapsed >= 2 ? total + (total / elapsed) * remaining : null;
  const used = quota > 0 ? (total / quota) * 100 : null;
  const tone = used == null ? "" : used >= 100 ? "text-down" : used >= 80 ? "text-warn" : "";

  return (
    <Card>
      <CardTitle aside={<span className="text-xs text-fg-3">{fmtDay(cycle.start)} – {fmtDay(cycle.end - DAY)} (UTC)</span>}>Traffic this cycle</CardTitle>
      <div className="grid grid-cols-3 gap-3">
        <Total label="In" value={cycle.rx} color={IN} />
        <Total label="Out" value={cycle.tx} color={OUT} />
        <Total label="Total" value={total} />
      </div>
      {used != null ? (
        <div className="mt-4">
          <div className="mb-1.5 flex items-baseline justify-between text-xs">
            <span className="text-fg-2">
              {fmtBytes(total)} of {cycle.quotaGb.toLocaleString()} GB allowance
            </span>
            <span className={`tnum font-mono ${tone}`}>{used.toFixed(used < 10 ? 1 : 0)}%</span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-surface-3">
            <div className="h-full rounded-full" style={{ width: `${Math.min(100, used)}%`, background: used >= 100 ? "var(--down)" : used >= 80 ? "var(--warn)" : "var(--series)" }} />
          </div>
          {projected != null && (
            <p className={`mt-1.5 text-xs ${projected > quota ? "text-warn" : "text-fg-3"}`}>
              On pace for about {fmtBytes(projected)} by the end of the cycle{projected > quota ? ", over the allowance" : ""}.
            </p>
          )}
        </div>
      ) : (
        <p className="mt-3 text-xs text-fg-3">
          {projected != null && <>On pace for about {fmtBytes(projected)} this cycle. </>}Set the provider's monthly allowance under <b>Edit &amp; alerts</b> to track it.
        </p>
      )}
      {partial && <p className="mt-1.5 text-xs text-fg-3">Counting since {fmtDay(first!)}; earlier traffic this cycle wasn't recorded.</p>}
      <h3 className="mt-5 mb-2 text-xs font-medium text-fg-2">Last 30 days</h3>
      <DailyBars days={n.days} />
    </Card>
  );
}

function Total({ label, value, color }: { label: string; value: number; color?: string }) {
  return (
    <div>
      <div className="flex items-center gap-1.5 text-xs text-fg-2">
        {color && <span className="size-2 rounded-full" style={{ background: color }} />}
        {label}
      </div>
      <div className="tnum mt-1 font-mono text-lg tracking-[-0.03em]">{fmtBytes(value)}</div>
    </div>
  );
}

/** Mirrored daily bars: in above the axis, out below. */
function DailyBars({ days }: { days: ServerNetwork["days"] }) {
  const [ref, { w }] = useSize();
  const tip = useTip();
  const H = 120;
  const series = useMemo(() => {
    const by = new Map(days.map((d) => [d.day, d]));
    const today = Math.floor(Date.now() / DAY) * DAY;
    return Array.from({ length: 30 }, (_, i) => {
      const day = today - (29 - i) * DAY;
      return by.get(day) ?? { day, rx: 0, tx: 0 };
    });
  }, [days]);
  const max = Math.max(1, ...series.map((d) => Math.max(d.rx, d.tx)));
  const gap = 2;
  const bw = w > 0 ? Math.max(2, (w - gap * 29) / 30) : 0;
  const half = H / 2 - 1;
  return (
    <div ref={ref}>
      <svg width="100%" height={H} role="img" aria-label="Daily traffic for the last 30 days">
        <line x1={0} x2={w} y1={H / 2} y2={H / 2} stroke="var(--line-strong)" />
        {w > 0 &&
          series.map((d, i) => {
            const x = i * (bw + gap);
            const up = (d.rx / max) * half, dn = (d.tx / max) * half;
            return (
              <g
                key={d.day}
                onPointerEnter={(e) => tip.show(e.clientX, e.clientY, { title: fmtDay(d.day), rows: [{ label: "In", value: fmtBytes(d.rx), color: IN }, { label: "Out", value: fmtBytes(d.tx), color: OUT }] })}
                onPointerLeave={tip.hide}
              >
                <rect x={x} y={0} width={bw} height={H} fill="transparent" />
                {up > 0 && <rect x={x} y={H / 2 - 1 - Math.max(2, up)} width={bw} height={Math.max(2, up)} rx={Math.min(3, bw / 2)} fill={IN} />}
                {dn > 0 && <rect x={x} y={H / 2 + 1} width={bw} height={Math.max(2, dn)} rx={Math.min(3, bw / 2)} fill={OUT} />}
              </g>
            );
          })}
      </svg>
      <div className="mt-1.5 flex items-center justify-between text-[11px] text-fg-3">
        <span>{fmtDay(series[0].day)}</span>
        <span className="flex gap-3">
          <span className="flex items-center gap-1">
            <span className="size-2 rounded-full" style={{ background: IN }} />
            In
          </span>
          <span className="flex items-center gap-1">
            <span className="size-2 rounded-full" style={{ background: OUT }} />
            Out
          </span>
        </span>
        <span>today</span>
      </div>
    </div>
  );
}

function SpeedTestCard({ serverId, n, online, off }: { serverId: number; n: ServerNetwork; online: boolean; off: boolean }) {
  const qc = useQueryClient();
  const [error, setError] = useState("");
  const run = useMutation({
    mutationFn: () => api.post<SpeedTest>(`/servers/${serverId}/speedtest`),
    onSuccess: (t) => {
      setError(t.error ?? "");
      qc.invalidateQueries({ queryKey: ["serverNetwork", serverId] });
    },
    onError: (e) => setError((e as Error).message),
  });
  const ok = n.speedTests.filter((t) => !t.error);
  const last = ok[0];
  const every = n.speedTestHours;

  return (
    <Card>
      <CardTitle aside={<span className="text-xs text-fg-3">{every ? `automatic every ${every === 168 ? "week" : `${every} h`}` : "on demand"}</span>}>Speed test</CardTitle>
      {last ? (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <Big label="Download" value={fmtBits(last.downBps)} color={IN} />
            <Big label="Upload" value={fmtBits(last.upBps)} color={OUT} />
            <Big label="Latency" value={`${last.latencyMs.toFixed(last.latencyMs < 10 ? 1 : 0)} ms`} />
            <Big label="Jitter" value={`${last.jitterMs.toFixed(1)} ms`} />
          </div>
          <p className="mt-2 text-xs text-fg-3">
            {last.server} · {fmtAgo(last.ts)} · {fmtBytes(last.bytes)} used
          </p>
        </>
      ) : (
        <p className="text-sm text-fg-2">No speed test yet. A test measures this server's own internet connection, not the hub's.</p>
      )}

      <div className="mt-4 flex flex-wrap items-center gap-3">
        <Button variant={last ? "secondary" : "primary"} loading={run.isPending} disabled={!online || off} onClick={() => run.mutate()}>
          {run.isPending ? "Testing… about 20 s" : "Run speed test"}
        </Button>
        <span className="text-xs text-fg-3">{off ? "Turned off on this server (HOOK_SPEEDTEST=0)." : !online ? "The server is offline." : "Uses up to about 350 MB of data."}</span>
      </div>
      {error && (
        <div className="mt-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}

      {n.speedTests.length > 1 && (
        <div className="-mx-1 mt-5 overflow-x-auto">
          <table className="w-full min-w-[380px] text-sm">
            <thead>
              <tr className="text-left text-xs text-fg-3">
                <th className="px-1 pb-2 font-medium">When</th>
                <th className="px-1 pb-2 text-right font-medium">Down</th>
                <th className="px-1 pb-2 text-right font-medium">Up</th>
                <th className="px-1 pb-2 text-right font-medium">Latency</th>
              </tr>
            </thead>
            <tbody>
              {n.speedTests.slice(0, 10).map((t) => (
                <tr key={t.id} className="border-t border-line">
                  <td className="px-1 py-2 text-xs text-fg-2">
                    {fmtAgo(t.ts)}
                    {t.scheduled && <span className="text-fg-3"> · auto</span>}
                  </td>
                  {t.error ? (
                    <td colSpan={3} className="truncate px-1 py-2 text-right text-xs text-down" title={t.error}>
                      {t.error}
                    </td>
                  ) : (
                    <>
                      <td className="tnum px-1 py-2 text-right font-mono text-xs">{fmtBits(t.downBps)}</td>
                      <td className="tnum px-1 py-2 text-right font-mono text-xs">{fmtBits(t.upBps)}</td>
                      <td className="tnum px-1 py-2 text-right font-mono text-xs">{t.latencyMs.toFixed(1)} ms</td>
                    </>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function Big({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div>
      <div className="flex items-center gap-1.5 text-xs text-fg-2">
        {color && <span className="size-2 rounded-full" style={{ background: color }} />}
        {label}
      </div>
      <div className="tnum mt-1 font-mono text-xl tracking-[-0.03em]">{value}</div>
    </div>
  );
}

const fmtLink = (mbps?: number) => (!mbps ? "—" : mbps >= 1000 ? `${mbps / 1000} Gbit/s` : `${mbps} Mbit/s`);

export function InterfacesCard({ ifaces }: { ifaces: Iface[] }) {
  return (
    <Card className="lg:col-span-2">
      <CardTitle aside={<span className="text-xs text-fg-3">live · every 30s</span>}>Network interfaces</CardTitle>
      {ifaces.length === 0 ? (
        <p className="text-sm text-fg-2">No physical interfaces reported.</p>
      ) : (
        <div className="-mx-1 overflow-x-auto">
          <table className="w-full min-w-[640px] text-sm">
            <thead>
              <tr className="text-left text-xs text-fg-3">
                <th className="px-1 pb-2 font-medium">Interface</th>
                <th className="px-1 pb-2 font-medium">Addresses</th>
                <th className="px-1 pb-2 text-right font-medium">Link</th>
                <th className="px-1 pb-2 text-right font-medium">In</th>
                <th className="px-1 pb-2 text-right font-medium">Out</th>
                <th className="px-1 pb-2 text-right font-medium">Since boot</th>
                <th className="px-1 pb-2 text-right font-medium">Errors</th>
              </tr>
            </thead>
            <tbody>
              {ifaces.map((f) => {
                const errs = f.rxErr + f.txErr + f.drops;
                return (
                  <tr key={f.name} className="border-t border-line align-top">
                    <td className="px-1 py-2">
                      <div className="flex items-center gap-2">
                        {f.up != null && <span className={`size-2 shrink-0 rounded-full ${f.up ? "bg-up" : "bg-down"}`} title={f.up ? "up" : "down"} />}
                        <span className="font-mono text-[13px]">{f.name}</span>
                      </div>
                      {f.mtu ? <div className="mt-0.5 text-[11px] text-fg-3">MTU {f.mtu}</div> : null}
                    </td>
                    <td className="max-w-[240px] px-1 py-2">
                      {f.addrs && f.addrs.length > 0 ? (
                        <div className="flex flex-wrap gap-1">
                          {f.addrs.map((a) => (
                            <Chip key={a} className="font-mono text-[11px]">
                              {a}
                            </Chip>
                          ))}
                        </div>
                      ) : (
                        <span className="text-xs text-fg-3">—</span>
                      )}
                    </td>
                    <td className="tnum px-1 py-2 text-right font-mono text-xs text-fg-2">{fmtLink(f.speed)}</td>
                    <td className="tnum px-1 py-2 text-right font-mono text-xs">{fmtRate(f.rx)}</td>
                    <td className="tnum px-1 py-2 text-right font-mono text-xs">{fmtRate(f.tx)}</td>
                    <td className="tnum px-1 py-2 text-right font-mono text-xs text-fg-2">{f.rxTotal != null ? `↓ ${fmtBytes(f.rxTotal)} ↑ ${fmtBytes(f.txTotal ?? 0)}` : "—"}</td>
                    <td className={`tnum px-1 py-2 text-right font-mono text-xs ${errs > 0 ? "text-warn" : "text-fg-3"}`}>{errs}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
