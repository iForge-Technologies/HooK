import type { GPU, GPUs } from "../lib/api";
import { fmtBytes, fmtPct } from "../lib/format";
import { TickGauge } from "./server-charts";
import { Card, CardTitle, Chip } from "./ui";

const VENDOR: Record<GPU["vendor"], string> = { nvidia: "NVIDIA", amd: "AMD", intel: "Intel" };

/** GPU devices and their processes; says plainly when a server has none. */
export function GPUCard({ gpu }: { gpu: GPUs | undefined }) {
  if (!gpu) {
    return (
      <Card>
        <CardTitle>GPU</CardTitle>
        <p className="text-sm text-fg-2">This agent version doesn't report GPUs. Reinstall the agent to update it.</p>
      </Card>
    );
  }
  if (!gpu.present) {
    return (
      <Card>
        <CardTitle aside={<span className="text-xs text-fg-3">none</span>}>GPU</CardTitle>
        <div className="flex items-center gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-surface-2 font-mono text-xs font-semibold text-fg-3">NO</span>
          <div>
            <p className="font-semibold">No GPU</p>
            <p className="text-sm text-fg-2">{gpu.note || "This server has no graphics card, only a virtual display adapter if any."}</p>
          </div>
        </div>
      </Card>
    );
  }
  const procs = gpu.procs ?? [];
  return (
    <Card className="lg:col-span-2">
      <CardTitle aside={<span className="text-xs text-fg-3">{gpu.devices.length === 1 ? "1 device" : `${gpu.devices.length} devices`} · every 30s</span>}>GPU</CardTitle>
      {gpu.note && <p className="mb-3 text-xs text-warn">{gpu.note}</p>}
      <div className="grid gap-4 md:grid-cols-2">
        {gpu.devices.map((d) => (
          <Device key={`${d.vendor}-${d.index}`} d={d} />
        ))}
      </div>
      {procs.length > 0 && (
        <div className="-mx-1 mt-5 overflow-x-auto">
          <table className="w-full min-w-[320px] text-sm">
            <thead>
              <tr className="text-left text-xs text-fg-3">
                <th className="px-1 pb-2 font-medium">Process</th>
                <th className="px-1 pb-2 font-medium">GPU</th>
                <th className="px-1 pb-2 text-right font-medium">GPU memory</th>
              </tr>
            </thead>
            <tbody>
              {procs.map((p) => (
                <tr key={`${p.pid}-${p.gpu}`} className="border-t border-line">
                  <td className="max-w-[220px] truncate px-1 py-2 font-mono text-[13px]" title={`PID ${p.pid}`}>
                    {p.name || `pid ${p.pid}`}
                  </td>
                  <td className="px-1 py-2 text-xs text-fg-2">#{p.gpu}</td>
                  <td className="tnum px-1 py-2 text-right font-mono text-xs">{p.mem > 0 ? fmtBytes(p.mem) : <span className="text-fg-3" title="The driver doesn't report per-process memory (common on Windows)">—</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function Device({ d }: { d: GPU }) {
  const memPct = d.memTotal > 0 ? (d.memUsed / d.memTotal) * 100 : null;
  const stats: [string, string, boolean?][] = [];
  if (d.temp) stats.push(["Temperature", `${Math.round(d.temp)}°C`, d.temp >= 85]);
  if (d.power) stats.push(["Power", d.powerLimit ? `${Math.round(d.power)} / ${Math.round(d.powerLimit)} W` : `${Math.round(d.power)} W`]);
  if (d.fan) stats.push(["Fan", `${Math.round(d.fan)}%`]);
  if (d.clockMhz) stats.push(["Clock", `${Math.round(d.clockMhz)} MHz`]);
  return (
    <div className="rounded-2xl border border-line p-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1 truncate font-semibold tracking-[-0.01em]">{d.name}</span>
        <Chip>{VENDOR[d.vendor]}</Chip>
        {d.driver && <Chip>driver {d.driver}</Chip>}
      </div>
      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        {d.util >= 0 ? (
          <TickGauge label="Utilisation" value={d.util} display={fmtPct(d.util)} ticks={20} />
        ) : (
          <div className="text-sm">
            <div className="text-xs font-medium text-fg-2">Utilisation</div>
            <p className="mt-1.5 text-xs text-fg-3">Not reported by this driver.</p>
          </div>
        )}
        {memPct != null ? (
          <TickGauge label="Memory" value={memPct} display={fmtPct(memPct)} sub={`${fmtBytes(d.memUsed)} of ${fmtBytes(d.memTotal)}`} ticks={20} />
        ) : (
          <div className="text-sm">
            <div className="text-xs font-medium text-fg-2">Memory</div>
            <p className="mt-1.5 text-xs text-fg-3">Shares system memory.</p>
          </div>
        )}
      </div>
      {stats.length > 0 && (
        <dl className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
          {stats.map(([k, v, hot]) => (
            <div key={k}>
              <dt className="text-xs text-fg-3">{k}</dt>
              <dd className={`tnum font-mono text-sm ${hot ? "text-warn" : ""}`}>{v}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}
