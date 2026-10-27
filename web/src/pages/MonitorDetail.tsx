import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useLocation } from "wouter";
import { HeartbeatBar, ResponseChart, Waterfall } from "../components/charts";
import { Modal, useToast } from "../components/overlay";
import { shownStatus, STATUS, StatusBadge, StatusIcon } from "../components/status";
import { Button, Card, CardTitle, Chip, Segmented, Spinner } from "../components/ui";
import { api, type Meta, type MonitorDetail, type Series } from "../lib/api";
import { InstallCommand } from "./ServerModals";
import { daysUntil, fmtDateTime, fmtDuration, fmtInterval, fmtMs, fmtStamp, fmtUptime, TYPE_LABEL } from "../lib/format";
import { useNow } from "../lib/hooks";

type Range = "1h" | "24h" | "7d" | "30d" | "90d";

export function MonitorDetailPage({ id }: { id: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [, navigate] = useLocation();
  const [range, setRange] = useState<Range>("24h");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const now = useNow(1000);

  const q = useQuery({ queryKey: ["monitor", id], queryFn: () => api.get<MonitorDetail>(`/monitors/${id}`), refetchInterval: 60_000 });
  const series = useQuery({
    queryKey: ["series", id, range],
    queryFn: () => api.get<Series>(`/monitors/${id}/series?range=${range}`),
    placeholderData: (prev) => prev,
    refetchInterval: range === "1h" || range === "24h" ? false : 300_000,
  });

  const act = useMutation({
    mutationFn: (action: "pause" | "resume" | "check") => api.post(`/monitors/${id}/${action}`),
    onSuccess: (_, action) => {
      if (action === "check") toast("Check started");
      else {
        toast(action === "pause" ? "Monitor paused" : "Monitor resumed");
        qc.invalidateQueries({ queryKey: ["monitor", id] });
        qc.invalidateQueries({ queryKey: ["monitors"] });
      }
    },
    onError: (e) => toast((e as Error).message, "error"),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/monitors/${id}`),
    onSuccess: () => {
      toast("Monitor deleted");
      qc.removeQueries({ queryKey: ["monitor", id] });
      qc.invalidateQueries({ queryKey: ["monitors"] });
      navigate("/monitors");
    },
    onError: (e) => toast((e as Error).message, "error"),
  });

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
        <p className="font-semibold">Couldn't load this monitor.</p>
        <p className="mt-1 text-sm text-fg-2">{(q.error as Error).message}</p>
        <Link href="/monitors" className="mt-4 inline-block text-sm text-series">
          ← Back to monitors
        </Link>
      </Card>
    );
  }

  const { monitor: m, uptime, avgLatency24h, beats, incidents } = q.data;
  const status = shownStatus(m);
  const lastTimed = [...beats].reverse().find((b) => b.timings);
  const certDays = m.certExpires ? daysUntil(m.certExpires) : null;
  const recent = [...beats].reverse().slice(0, 25);
  const isUrl = m.type === "http" || m.type === "keyword";

  return (
    <div className="flex flex-col gap-4 sm:gap-5">
      <Link href="/monitors" className="inline-flex w-fit items-center gap-1 text-sm text-fg-2 hover:text-fg">
        <svg viewBox="0 0 16 16" className="size-3.5" aria-hidden>
          <path d="M10 3.5L5.5 8l4.5 4.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        Monitors
      </Link>

      {/* header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="truncate text-[28px] font-bold tracking-[-0.03em]">{m.name}</h1>
            <StatusBadge status={status} size="lg" pulse />
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-sm text-fg-2">
            {isUrl ? (
              <a href={m.target} target="_blank" rel="noreferrer noopener" className="truncate font-mono text-[13px] hover:text-fg hover:underline">
                {m.target}
              </a>
            ) : (
              <span className="truncate font-mono text-[13px]">{m.target}</span>
            )}
            <Chip>{TYPE_LABEL[m.type]}</Chip>
            {m.group && <Chip>{m.group}</Chip>}
            <Chip>every {fmtInterval(m.interval)}</Chip>
          </div>
          <p className="mt-2 text-sm text-fg-3">
            {status === "new" ? "Waiting for the first check" : status === "paused" ? "Paused — no checks are running" : `${STATUS[status].label} for ${fmtDuration(now - m.statusSince)}`}
            {m.lastMsg && m.active && <span className="text-fg-2"> · {m.lastMsg}</span>}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {m.active && m.type !== "push" && (
            <Button size="sm" onClick={() => act.mutate("check")} loading={act.isPending && act.variables === "check"}>
              Check now
            </Button>
          )}
          <Button size="sm" onClick={() => act.mutate(m.active ? "pause" : "resume")} loading={act.isPending && act.variables !== "check"}>
            {m.active ? "Pause" : "Resume"}
          </Button>
          <Link href={`/monitors/${id}/edit`}>
            <Button size="sm">Edit</Button>
          </Link>
          <Button size="sm" variant="ghost" className="text-down hover:text-down" onClick={() => setConfirmDelete(true)}>
            Delete
          </Button>
        </div>
      </div>

      {/* stat tiles */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Tile label="Response" value={status === "down" || status === "new" ? "—" : fmtMs(m.lastLatency)} note="last check" />
        <Tile label="Avg response" value={fmtMs(avgLatency24h)} note="24 hours" />
        <Tile label="Uptime" value={fmtUptime(uptime["24h"])} note="24 hours" />
        <Tile label="Uptime" value={fmtUptime(uptime["7d"])} note="7 days" />
        <Tile label="Uptime" value={fmtUptime(uptime["30d"])} note="30 days" />
        {certDays !== null ? (
          <Tile label="Certificate" value={certDays < 0 ? "Expired" : `${certDays} days`} note={m.certIssuer || "until expiry"} warn={certDays <= 14} />
        ) : (
          <Tile label="Uptime" value={fmtUptime(uptime["90d"])} note="90 days" />
        )}
      </div>

      {m.type === "push" && m.config.pushToken && <PushCard id={id} token={m.config.pushToken} />}

      <Card>
        <CardTitle aside={<span className="text-xs text-fg-3">last {Math.min(beats.length, 100)} checks</span>}>Heartbeat</CardTitle>
        {beats.length ? <HeartbeatBar beats={beats} height={34} /> : <p className="text-sm text-fg-2">Waiting for the first check…</p>}
        <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1.5 text-xs text-fg-2">
          {(["up", "degraded", "pending", "down"] as const).map((s) => (
            <span key={s} className="inline-flex items-center gap-1.5">
              <StatusIcon status={s} className="size-3" />
              {STATUS[s].label}
            </span>
          ))}
        </div>
      </Card>

      <Card>
        <CardTitle
          aside={
            <Segmented
              label="Time range"
              value={range}
              onChange={setRange}
              items={(["1h", "24h", "7d", "30d", "90d"] as const).map((r) => ({ value: r, label: r }))}
            />
          }
        >
          Response time
        </CardTitle>
        {series.data && series.data.points.length > 1 ? (
          <ResponseChart points={series.data.points} hourly={series.data.resolution === "1h"} />
        ) : (
          <div className="grid h-[240px] place-items-center text-sm text-fg-2">{series.isPending ? <Spinner /> : "Not enough data for this range yet."}</div>
        )}
        {series.data?.resolution === "1h" && <p className="mt-2 text-xs text-fg-3">Hourly averages. Red bands mark hours with failed checks.</p>}
      </Card>

      <div className="grid gap-4 sm:gap-5 lg:grid-cols-2">
        {isUrl && lastTimed?.timings && (
          <Card className="lg:col-span-2">
            <CardTitle aside={<span className="text-xs text-fg-3">{fmtStamp(lastTimed.ts)}</span>}>Request breakdown</CardTitle>
            <Waterfall timings={lastTimed.timings} />
          </Card>
        )}

        <Card>
          <CardTitle aside={<span className="text-xs text-fg-3">{incidents.length ? `${incidents.length} recent` : ""}</span>}>Incidents</CardTitle>
          {incidents.length === 0 ? (
            <p className="text-sm text-fg-2">No outages recorded.</p>
          ) : (
            <ul className="-my-1">
              {incidents.map((inc) => {
                const open = inc.resolvedAt == null;
                return (
                  <li key={inc.id} className="flex items-start gap-3 border-t border-line py-3 first:border-t-0">
                    <StatusIcon status={open ? "down" : "up"} className="mt-0.5 size-4" />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                        <span className="text-sm font-medium">{open ? "Ongoing outage" : `Down for ${fmtDuration(inc.resolvedAt! - inc.startedAt)}`}</span>
                        <span className="text-xs text-fg-3">{fmtDateTime(inc.startedAt)}</span>
                      </div>
                      <p className="mt-0.5 break-words text-xs text-fg-2">{inc.cause || "No reason recorded"}</p>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>

        <Card>
          <CardTitle>Recent checks</CardTitle>
          <div className="-mx-1 overflow-x-auto">
            <table className="w-full min-w-[420px] border-collapse text-sm">
              <thead>
                <tr className="text-left text-xs text-fg-3">
                  <th className="px-1 pb-2 font-medium">Time</th>
                  <th className="px-1 pb-2 font-medium">Status</th>
                  <th className="px-1 pb-2 text-right font-medium">Response</th>
                  <th className="px-1 pb-2 font-medium">Detail</th>
                </tr>
              </thead>
              <tbody>
                {recent.map((b) => (
                  <tr key={b.ts} className="border-t border-line">
                    <td className="tnum whitespace-nowrap px-1 py-2 font-mono text-xs text-fg-2">{fmtStamp(b.ts)}</td>
                    <td className="px-1 py-2">
                      <span className="inline-flex items-center gap-1.5 text-xs">
                        <StatusIcon status={b.status} className="size-3" />
                        {STATUS[b.status].label}
                      </span>
                    </td>
                    <td className="tnum whitespace-nowrap px-1 py-2 text-right font-mono text-xs">{b.status === "down" ? "—" : fmtMs(b.latency)}</td>
                    <td className="max-w-[220px] truncate px-1 py-2 text-xs text-fg-2" title={b.msg}>
                      {b.msg}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {recent.length === 0 && <p className="py-4 text-sm text-fg-2">No checks yet.</p>}
          </div>
        </Card>
      </div>

      <Modal open={confirmDelete} onClose={() => setConfirmDelete(false)} title="Delete monitor?" width="max-w-sm">
        <p className="text-sm text-fg-2">
          This removes <b className="text-fg">{m.name}</b> and all of its history. It can't be undone.
        </p>
        <div className="mt-6 flex justify-end gap-2">
          <Button onClick={() => setConfirmDelete(false)}>Cancel</Button>
          <Button variant="danger" loading={del.isPending} onClick={() => del.mutate()}>
            Delete
          </Button>
        </div>
      </Modal>
    </div>
  );
}

function Tile({ label, value, note, warn }: { label: string; value: string; note: string; warn?: boolean }) {
  return (
    <div className="rounded-[20px] border border-line bg-surface p-4">
      <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-[0.06em] text-fg-2">
        {warn && <StatusIcon status="degraded" className="size-3" />}
        {label}
      </div>
      <div className="tnum mt-2 font-mono text-xl tracking-[-0.03em]">{value}</div>
      <div className="mt-0.5 truncate text-xs text-fg-3">{note}</div>
    </div>
  );
}

/** The push URL with ready-to-paste examples. */
function PushCard({ id, token }: { id: number; token: string }) {
  const qc = useQueryClient();
  const toast = useToast();
  const meta = useQuery({ queryKey: ["meta"], queryFn: () => api.get<Meta>("/meta") });
  const [confirm, setConfirm] = useState(false);
  const base = meta.data?.publicUrl || window.location.origin;
  const url = `${base}/api/push/${token}`;
  const rotate = useMutation({
    mutationFn: () => api.post(`/monitors/${id}/push-token`),
    onSuccess: () => {
      setConfirm(false);
      toast("New push URL created; the old one stopped working");
      qc.invalidateQueries({ queryKey: ["monitor", id] });
    },
    onError: (e) => toast((e as Error).message, "error"),
  });
  return (
    <Card>
      <CardTitle
        aside={
          confirm ? (
            <div className="flex gap-2">
              <Button size="sm" variant="danger" loading={rotate.isPending} onClick={() => rotate.mutate()}>
                Replace URL
              </Button>
              <Button size="sm" onClick={() => setConfirm(false)}>
                Cancel
              </Button>
            </div>
          ) : (
            <Button size="sm" variant="ghost" onClick={() => setConfirm(true)}>
              New URL…
            </Button>
          )
        }
      >
        Push URL
      </CardTitle>
      <InstallCommand command={url} />
      <div className="mt-4 grid gap-3 text-sm md:grid-cols-2">
        <div>
          <div className="mb-1.5 text-xs font-medium text-fg-2">After a cron job succeeds</div>
          <InstallCommand command={`0 3 * * * /opt/backup.sh && curl -fsS -m 10 --retry 3 ${url} > /dev/null`} />
        </div>
        <div>
          <div className="mb-1.5 text-xs font-medium text-fg-2">Report a failure (or timing) explicitly</div>
          <InstallCommand command={`curl -fsS "${url}?status=down&msg=backup+failed"`} />
        </div>
      </div>
      <p className="mt-3 text-xs text-fg-3">GET or POST both work. Optional query: status=up|down, msg=…, ping=milliseconds. Anyone with this URL can report for this monitor, so keep it private.</p>
    </Card>
  );
}
