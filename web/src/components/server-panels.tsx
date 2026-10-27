import { Fragment, useState, type ReactNode } from "react";
import type { Container, Docker, DockerImage, DockerVolume, Status, Unit } from "../lib/api";
import { fmtAgo, fmtBytes, fmtDateTime, fmtDuration, fmtRate } from "../lib/format";
import { StatusIcon } from "./status";
import { Card, CardTitle, Chip, Segmented } from "./ui";

function containerStatus(c: Container): Status {
  if (c.state === "running") return c.health === "unhealthy" ? "down" : c.health === "starting" ? "pending" : "up";
  if (c.state === "restarting") return "down";
  return "paused"; // exited, created, dead, paused: shown, but only alerted after being seen running
}

export function DockerCard({ docker }: { docker: Docker }) {
  const [tab, setTab] = useState<"containers" | "images" | "volumes">("containers");
  const cs = docker.containers ?? [];
  const images = docker.images ?? [];
  const volumes = docker.volumes ?? [];
  const running = cs.filter((c) => c.state === "running").length;
  const u = docker.usage;
  return (
    <Card className="lg:col-span-2">
      <CardTitle
        aside={
          docker.available ? (
            <span className="text-xs text-fg-3">
              {docker.version && `Engine ${docker.version} · `}
              {running} running · {cs.length} total
            </span>
          ) : undefined
        }
      >
        Docker
      </CardTitle>
      {!docker.available ? (
        <div className="text-sm text-fg-2">
          <p>{docker.error}</p>
          <p className="mt-2 text-xs text-fg-3">
            Reinstall the agent with <code className="font-mono">--docker-host tcp://127.0.0.1:2375</code> pointing at a read-only socket proxy (recommended), or with{" "}
            <code className="font-mono">--docker</code> to add it to the docker group.
          </p>
        </div>
      ) : (
        <>
          {u && (
            <dl className="mb-5 grid grid-cols-2 gap-3 sm:grid-cols-5">
              <UsageStat label="Images" value={fmtBytes(u.images)} />
              <UsageStat label="Containers" value={fmtBytes(u.containers)} hint="writable layers" />
              <UsageStat label="Volumes" value={u.volumes < 0 ? "—" : fmtBytes(u.volumes)} />
              <UsageStat label="Build cache" value={fmtBytes(u.buildCache)} />
              <UsageStat label="Reclaimable" value={`~${fmtBytes(u.reclaimable)}`} hint={`as of ${fmtAgo(u.at)}`} />
            </dl>
          )}
          <div className="mb-3">
            <Segmented
              label="Docker objects"
              value={tab}
              onChange={setTab}
              items={[
                { value: "containers", label: `Containers ${cs.length}` },
                { value: "images", label: `Images ${images.length}` },
                { value: "volumes", label: `Volumes ${volumes.length}` },
              ]}
            />
          </div>
          {tab === "containers" && <ContainerTable cs={cs} />}
          {tab === "images" && <ImageTable images={images} />}
          {tab === "volumes" && <VolumeTable volumes={volumes} />}
          {docker.detailError && (tab !== "containers" || !u) && (
            <p className="mt-3 text-xs text-fg-3">
              Some details are missing: {docker.detailError}. With a socket proxy, allow <code className="font-mono">IMAGES=1 VOLUMES=1 SYSTEM=1</code> (all read-only).
            </p>
          )}
        </>
      )}
    </Card>
  );
}

function UsageStat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="rounded-2xl bg-surface-2 px-3 py-2.5">
      <dt className="text-xs text-fg-2">{label}</dt>
      <dd className="tnum mt-0.5 font-mono text-[15px] tracking-[-0.02em]">{value}</dd>
      {hint && <dd className="truncate text-[11px] text-fg-3">{hint}</dd>}
    </div>
  );
}

function ContainerTable({ cs }: { cs: Container[] }) {
  const [open, setOpen] = useState<string | null>(null);
  if (cs.length === 0) return <p className="text-sm text-fg-2">No containers on this server.</p>;
  return (
    <div className="-mx-1 overflow-x-auto">
      <table className="w-full min-w-[760px] text-sm">
        <thead>
          <tr className="text-left text-xs text-fg-3">
            <th className="px-1 pb-2 font-medium">Container</th>
            <th className="px-1 pb-2 font-medium">State</th>
            <th className="px-1 pb-2 text-right font-medium">CPU</th>
            <th className="px-1 pb-2 text-right font-medium">Memory</th>
            <th className="px-1 pb-2 text-right font-medium">Net I/O</th>
            <th className="px-1 pb-2 text-right font-medium">Disk I/O</th>
            <th className="px-1 pb-2 text-right font-medium">PIDs</th>
            <th className="px-1 pb-2 text-right font-medium">Restarts</th>
          </tr>
        </thead>
        <tbody>
          {cs.map((c) => {
            const run = c.state === "running";
            const expanded = open === c.id;
            return (
              <Fragment key={c.id}>
                <tr className="cursor-pointer border-t border-line hover:bg-surface-2/60" onClick={() => setOpen(expanded ? null : c.id)} aria-expanded={expanded}>
                  <td className="max-w-[240px] px-1 py-2">
                    <div className="flex items-center gap-1.5">
                      <svg viewBox="0 0 16 16" className={`size-3 shrink-0 text-fg-3 transition-transform ${expanded ? "rotate-90" : ""}`} aria-hidden>
                        <path d="M6 3.5L10.5 8 6 12.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
                      </svg>
                      <span className="truncate font-mono text-[13px]">{c.name}</span>
                    </div>
                    <div className="truncate pl-[18px] text-xs text-fg-3">{c.image}</div>
                  </td>
                  <td className="px-1 py-2">
                    <span className="inline-flex items-center gap-1.5 text-xs" title={c.status}>
                      <StatusIcon status={containerStatus(c)} className="size-3.5" />
                      {c.state}
                      {c.health && <span className="text-fg-3">· {c.health}</span>}
                    </span>
                    {run && c.startedAt ? <div className="mt-0.5 text-[11px] text-fg-3">up {fmtDuration(Date.now() - c.startedAt)}</div> : null}
                  </td>
                  <td className="tnum px-1 py-2 text-right font-mono text-xs">{run ? `${c.cpu.toFixed(1)}%` : "—"}</td>
                  <td className="tnum px-1 py-2 text-right font-mono text-xs">
                    {run ? fmtBytes(c.mem) : "—"}
                    {run && c.memLimit > 0 && c.memLimit < 2 ** 50 && <div className="text-[11px] text-fg-3">of {fmtBytes(c.memLimit)}</div>}
                  </td>
                  <td className="tnum px-1 py-2 text-right font-mono text-xs">{run && c.netRx != null ? <IO a={c.netRx} b={c.netTx ?? 0} /> : "—"}</td>
                  <td className="tnum px-1 py-2 text-right font-mono text-xs">{run && c.blockRead != null ? <IO a={c.blockRead} b={c.blockWrite ?? 0} labels={["r", "w"]} /> : "—"}</td>
                  <td className="tnum px-1 py-2 text-right font-mono text-xs">{run && c.pids ? c.pids : "—"}</td>
                  <td className={`tnum px-1 py-2 text-right font-mono text-xs ${c.restarts > 0 ? "text-warn" : "text-fg-3"}`}>{c.restarts}</td>
                </tr>
                {expanded && (
                  <tr className="bg-surface-2/40">
                    <td colSpan={8} className="px-3 py-3">
                      <ContainerDetail c={c} />
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function IO({ a, b, labels = ["↓", "↑"] }: { a: number; b: number; labels?: [string, string] }) {
  return (
    <>
      <div>
        <span className="text-fg-3">{labels[0]}</span> {fmtRate(a)}
      </div>
      <div>
        <span className="text-fg-3">{labels[1]}</span> {fmtRate(b)}
      </div>
    </>
  );
}

const shortVolume = (name: string) => (/^[0-9a-f]{64}$/.test(name) ? name.slice(0, 12) : name);

function ContainerDetail({ c }: { c: Container }) {
  const rows: [string, ReactNode][] = [
    ["ID", <span className="font-mono">{c.id}</span>],
    ["Status", c.status],
  ];
  if (c.created) rows.push(["Created", fmtDateTime(c.created)]);
  if (c.sizeRw != null && c.sizeRw >= 0) rows.push(["Writable layer", fmtBytes(c.sizeRw)]);
  if (c.networks?.length) rows.push(["Networks", c.networks.join(", ")]);
  return (
    <div className="grid gap-4 text-xs md:grid-cols-3">
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5">
        {rows.map(([k, v]) => (
          <Fragment key={k}>
            <dt className="text-fg-3">{k}</dt>
            <dd className="min-w-0 break-words">{v}</dd>
          </Fragment>
        ))}
      </dl>
      <div>
        <div className="mb-1.5 text-fg-3">Ports</div>
        {c.ports?.length ? (
          <div className="flex flex-wrap gap-1">
            {c.ports.map((p) => (
              <Chip key={p} className="font-mono text-[11px]">
                {p}
              </Chip>
            ))}
          </div>
        ) : (
          <span className="text-fg-3">None published</span>
        )}
      </div>
      <div>
        <div className="mb-1.5 text-fg-3">Mounts</div>
        {c.mounts?.length ? (
          <ul className="space-y-1">
            {c.mounts.map((m) => (
              <li key={m.target} className="min-w-0 break-all font-mono text-[11px]">
                <span className="text-fg-3">{m.type}</span> {m.name ? (m.type === "volume" ? shortVolume(m.name) : m.name) : "—"} → {m.target}
                {!m.rw && <span className="text-fg-3"> (read-only)</span>}
              </li>
            ))}
          </ul>
        ) : (
          <span className="text-fg-3">None</span>
        )}
      </div>
    </div>
  );
}

function ImageTable({ images }: { images: DockerImage[] }) {
  if (images.length === 0) return <p className="text-sm text-fg-2">No images, or the agent hasn't listed them yet (it refreshes every 5 minutes).</p>;
  return (
    <div className="-mx-1 overflow-x-auto">
      <table className="w-full min-w-[520px] text-sm">
        <thead>
          <tr className="text-left text-xs text-fg-3">
            <th className="px-1 pb-2 font-medium">Image</th>
            <th className="px-1 pb-2 font-medium">ID</th>
            <th className="px-1 pb-2 text-right font-medium">Size</th>
            <th className="px-1 pb-2 text-right font-medium">Created</th>
            <th className="px-1 pb-2 text-right font-medium">Used by</th>
          </tr>
        </thead>
        <tbody>
          {images.map((im) => (
            <tr key={im.id} className="border-t border-line">
              <td className="max-w-[300px] px-1 py-2">
                {im.dangling ? (
                  <span className="inline-flex items-center gap-2 text-xs text-fg-2">
                    &lt;none&gt; <Chip>dangling</Chip>
                  </span>
                ) : (
                  im.tags.map((t) => (
                    <div key={t} className="truncate font-mono text-[13px]">
                      {t}
                    </div>
                  ))
                )}
              </td>
              <td className="px-1 py-2 font-mono text-xs text-fg-3">{im.id}</td>
              <td className="tnum px-1 py-2 text-right font-mono text-xs">{fmtBytes(im.size)}</td>
              <td className="px-1 py-2 text-right text-xs text-fg-2">{im.created ? fmtAgo(im.created) : "—"}</td>
              <td className="px-1 py-2 text-right text-xs">
                {im.containers < 0 ? <span className="text-fg-3">—</span> : im.containers === 0 ? <Chip>unused</Chip> : `${im.containers} container${im.containers === 1 ? "" : "s"}`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function VolumeTable({ volumes }: { volumes: DockerVolume[] }) {
  if (volumes.length === 0) return <p className="text-sm text-fg-2">No volumes, or the agent hasn't listed them yet (it refreshes every 5 minutes).</p>;
  return (
    <div className="-mx-1 overflow-x-auto">
      <table className="w-full min-w-[520px] text-sm">
        <thead>
          <tr className="text-left text-xs text-fg-3">
            <th className="px-1 pb-2 font-medium">Volume</th>
            <th className="px-1 pb-2 font-medium">Driver</th>
            <th className="px-1 pb-2 text-right font-medium">Size</th>
            <th className="px-1 pb-2 font-medium">Mounted by</th>
          </tr>
        </thead>
        <tbody>
          {volumes.map((v) => (
            <tr key={v.name} className="border-t border-line">
              <td className="max-w-[280px] px-1 py-2">
                <span className="truncate font-mono text-[13px]" title={v.name}>
                  {shortVolume(v.name)}
                </span>
                {v.anonymous && <Chip className="ml-2">anonymous</Chip>}
              </td>
              <td className="px-1 py-2 text-xs text-fg-2">{v.driver}</td>
              <td className="tnum px-1 py-2 text-right font-mono text-xs">{v.size < 0 ? "—" : fmtBytes(v.size)}</td>
              <td className="px-1 py-2 text-xs">{v.usedBy.length === 0 ? <Chip>unused</Chip> : <span className="font-mono text-[12px]">{v.usedBy.join(", ")}</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
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
