import type { Container, Snapshot, Status, Unit } from "../lib/api";
import { fmtBytes, fmtDateTime } from "../lib/format";
import { StatusIcon } from "./status";
import { Card, CardTitle } from "./ui";

function containerStatus(c: Container): Status {
  if (c.state === "running") return c.health === "unhealthy" ? "down" : c.health === "starting" ? "pending" : "up";
  if (c.state === "restarting") return "down";
  return "paused"; // exited, created, dead, paused: shown, but only alerted after being seen running
}

export function ContainersCard({ docker }: { docker: NonNullable<Snapshot["docker"]> }) {
  const cs = docker.containers ?? [];
  const running = cs.filter((c) => c.state === "running").length;
  return (
    <Card className="lg:col-span-2">
      <CardTitle aside={docker.available ? <span className="text-xs text-fg-3">{running} running · {cs.length} total</span> : undefined}>Containers</CardTitle>
      {!docker.available ? (
        <div className="text-sm text-fg-2">
          <p>{docker.error}</p>
          <p className="mt-2 text-xs text-fg-3">
            Reinstall the agent with <code className="font-mono">--docker-host tcp://127.0.0.1:2375</code> pointing at a read-only socket proxy (recommended), or with{" "}
            <code className="font-mono">--docker</code> to add it to the docker group.
          </p>
        </div>
      ) : cs.length === 0 ? (
        <p className="text-sm text-fg-2">No containers on this server.</p>
      ) : (
        <div className="-mx-1 overflow-x-auto">
          <table className="w-full min-w-[560px] text-sm">
            <thead>
              <tr className="text-left text-xs text-fg-3">
                <th className="px-1 pb-2 font-medium">Container</th>
                <th className="px-1 pb-2 font-medium">State</th>
                <th className="px-1 pb-2 text-right font-medium">CPU</th>
                <th className="px-1 pb-2 text-right font-medium">Memory</th>
                <th className="px-1 pb-2 text-right font-medium">Restarts</th>
              </tr>
            </thead>
            <tbody>
              {cs.map((c) => (
                <tr key={c.id} className="border-t border-line">
                  <td className="max-w-[260px] px-1 py-2">
                    <div className="truncate font-mono text-[13px]">{c.name}</div>
                    <div className="truncate text-xs text-fg-3">{c.image}</div>
                  </td>
                  <td className="px-1 py-2">
                    <span className="inline-flex items-center gap-1.5 text-xs" title={c.status}>
                      <StatusIcon status={containerStatus(c)} className="size-3.5" />
                      {c.state}
                      {c.health && <span className="text-fg-3">· {c.health}</span>}
                    </span>
                  </td>
                  <td className="tnum px-1 py-2 text-right font-mono text-xs">{c.state === "running" ? `${c.cpu.toFixed(1)}%` : "—"}</td>
                  <td className="tnum px-1 py-2 text-right font-mono text-xs">
                    {c.state === "running" ? fmtBytes(c.mem) : "—"}
                    {c.state === "running" && c.memLimit > 0 && c.memLimit < 2 ** 50 && <span className="text-fg-3"> / {fmtBytes(c.memLimit)}</span>}
                  </td>
                  <td className={`tnum px-1 py-2 text-right font-mono text-xs ${c.restarts > 0 ? "text-warn" : "text-fg-3"}`}>{c.restarts}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

export function ServicesCard({ units, failed, watched }: { units: Unit[]; failed: string[]; watched: string[] }) {
  const reported = new Set(units.map((u) => u.name));
  const pending = watched.filter((w) => !reported.has(w));
  return (
    <Card>
      <CardTitle aside={<span className="text-xs text-fg-3">systemd</span>}>Services</CardTitle>
      {watched.length === 0 && failed.length === 0 && (
        <p className="text-sm text-fg-2">
          No units watched yet. Add names like <code className="font-mono">nginx</code> or <code className="font-mono">postgresql</code> under <b>Edit &amp; alerts</b> to get alerted when they stop.
        </p>
      )}
      {units.length > 0 && (
        <ul className="-my-1">
          {units.map((u) => {
            const ok = u.active === "active" || u.active === "activating" || u.active === "reloading";
            return (
              <li key={u.name} className="flex items-center gap-3 border-t border-line py-2.5 first:border-t-0">
                <StatusIcon status={ok ? "up" : u.active === "not-found" ? "paused" : "down"} className="size-4" />
                <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{u.name}</span>
                <span className="text-xs text-fg-2">
                  {u.active} · {u.sub}
                </span>
                {u.since > 0 && <span className="hidden text-xs text-fg-3 sm:inline">since {fmtDateTime(u.since)}</span>}
                {u.restarts > 0 && <span className="text-xs text-warn">{u.restarts} restarts</span>}
              </li>
            );
          })}
        </ul>
      )}
      {pending.length > 0 && <p className="mt-2 text-xs text-fg-3">Waiting for the agent to report {pending.join(", ")}. Agents on Windows or without systemd can't.</p>}
      {failed.length > 0 && (
        <div className="mt-3 rounded-xl border border-down/30 bg-down/10 px-3 py-2 text-xs">
          <b>
            {failed.length} failed unit{failed.length > 1 ? "s" : ""} on this server:
          </b>{" "}
          <span className="font-mono">{failed.join(", ")}</span>
        </div>
      )}
    </Card>
  );
}
