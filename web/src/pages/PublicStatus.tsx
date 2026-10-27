import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import { useTip } from "../components/overlay";
import { STATUS, StatusIcon } from "../components/status";
import { DotMark, Spinner } from "../components/ui";
import { api, type PublicStatus, type Status } from "../lib/api";
import { fmtDuration, fmtMs, fmtUptime } from "../lib/format";
import { useSize } from "../lib/hooks";

const BANNER: Record<PublicStatus["overall"], { title: string; status: Status }> = {
  up: { title: "All systems operational", status: "up" },
  degraded: { title: "Some systems are degraded", status: "degraded" },
  down: { title: "Some systems are down", status: "down" },
  maintenance: { title: "Maintenance in progress", status: "maintenance" },
};

const fmtDay = (ms: number) => new Date(ms).toLocaleDateString([], { month: "short", day: "numeric", timeZone: "UTC" });

/** Public, unauthenticated page. It follows the visitor's system theme. */
export function PublicStatusPage({ slug }: { slug: string }) {
  const q = useQuery({ queryKey: ["public", slug], queryFn: () => api.get<PublicStatus>(`/public/status/${encodeURIComponent(slug)}`), refetchInterval: 30_000, retry: false });

  useEffect(() => {
    const m = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => document.documentElement.classList.toggle("light", !m.matches);
    apply();
    m.addEventListener("change", apply);
    return () => m.removeEventListener("change", apply);
  }, []);
  useEffect(() => {
    if (q.data) document.title = q.data.title;
  }, [q.data]);

  if (q.isPending) {
    return (
      <div className="grid min-h-dvh place-items-center">
        <Spinner className="size-5" />
      </div>
    );
  }
  if (q.isError) {
    return (
      <div className="grid min-h-dvh place-items-center px-6 text-center">
        <div>
          <p className="text-lg font-semibold">Status page not found</p>
          <p className="mt-1 text-sm text-fg-2">Check the address and try again.</p>
        </div>
      </div>
    );
  }
  const d = q.data;
  const b = BANNER[d.overall] ?? BANNER.up;
  const s = STATUS[b.status];
  const groups = new Map<string, PublicStatus["monitors"]>();
  for (const m of d.monitors) groups.set(m.group, [...(groups.get(m.group) ?? []), m]);

  return (
    <div className="mx-auto min-h-dvh max-w-3xl px-4 pb-16 pt-10 sm:px-6">
      <header>
        <h1 className="text-[30px] font-bold tracking-[-0.03em]">{d.title}</h1>
        {d.description && <p className="mt-1.5 text-fg-2">{d.description}</p>}
      </header>

      <section aria-live="polite" className="mt-6 flex items-center gap-3 rounded-[22px] p-5" style={{ background: `color-mix(in oklab, ${s.color} 16%, transparent)` }}>
        <StatusIcon status={b.status} className="size-6" />
        <div>
          <div className="text-lg font-semibold tracking-[-0.02em]">{b.title}</div>
          <div className="text-xs text-fg-2">Updated {new Date(d.updatedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} · refreshes every 30 seconds</div>
        </div>
      </section>

      {d.maintenance.length > 0 && (
        <section className="mt-4 flex flex-col gap-2">
          {d.maintenance.map((w, i) => (
            <div key={i} className="flex items-start gap-3 rounded-2xl border border-line bg-surface p-4">
              <StatusIcon status="maintenance" className="mt-0.5 size-4" />
              <div className="text-sm">
                <div className="font-semibold">{w.title}</div>
                <div className="text-fg-2">
                  {w.active ? "In progress, until " : "Scheduled "}
                  {new Date(w.active ? w.endsAt : w.startsAt).toLocaleString([], { weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}
                  {!w.active && ` for ${fmtDuration(w.endsAt - w.startsAt)}`}
                </div>
              </div>
            </div>
          ))}
        </section>
      )}

      {[...groups.entries()].map(([group, ms]) => (
        <section key={group || "_"} className="mt-6 overflow-hidden rounded-[22px] border border-line bg-surface">
          {group && <h2 className="px-5 pb-1 pt-4 text-[13px] font-semibold uppercase tracking-[0.05em] text-fg-2">{group}</h2>}
          <ul>
            {ms.map((m, i) => (
              <li key={i} className="border-t border-line px-5 py-4 first:border-t-0">
                <div className="flex items-center gap-2.5">
                  <StatusIcon status={m.status} className="size-4" />
                  <span className="flex-1 truncate font-medium">{m.name}</span>
                  {m.latency != null && <span className="font-mono text-xs text-fg-3">{fmtMs(m.latency)}</span>}
                  <span className="font-mono text-sm">{fmtUptime(m.uptime90)}</span>
                </div>
                <DayBars days={m.days} />
                <div className="mt-1 flex justify-between text-[11px] text-fg-3">
                  <span className="sm:hidden">30 days ago</span>
                  <span className="hidden sm:inline">90 days ago</span>
                  <span>Today</span>
                </div>
              </li>
            ))}
          </ul>
        </section>
      ))}

      <section className="mt-8">
        <h2 className="text-[15px] font-semibold tracking-[-0.02em]">Past incidents</h2>
        {d.incidents.length === 0 ? (
          <p className="mt-2 text-sm text-fg-2">No incidents in the last 14 days.</p>
        ) : (
          <ul className="mt-2">
            {d.incidents.map((inc, i) => (
              <li key={i} className="flex items-start gap-3 border-t border-line py-3 first:border-t-0">
                <StatusIcon status={inc.resolvedAt ? "up" : "down"} className="mt-0.5 size-4" />
                <div className="text-sm">
                  <div className="font-medium">
                    {inc.name} {inc.resolvedAt ? "was down" : "is down"}
                  </div>
                  <div className="text-fg-2">
                    {new Date(inc.startedAt).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}
                    {inc.resolvedAt ? ` · resolved after ${fmtDuration(inc.resolvedAt - inc.startedAt)}` : " · investigating"}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <footer className="mt-12 flex items-center justify-center gap-2 text-xs text-fg-3">
        <DotMark className="size-4" /> Powered by Hook
      </footer>
    </div>
  );
}

/** One pill per UTC day; 30 on phones, 90 from tablets up. */
function DayBars({ days }: { days: PublicStatus["monitors"][number]["days"] }) {
  const [ref, { w }] = useSize();
  const tip = useTip();
  const shown = useMemo(() => days.slice(w && w < 520 ? -30 : -90), [days, w]);
  const color = (u: number | null) => (u == null ? "var(--surface-3)" : u >= 0.999 ? "var(--up)" : u >= 0.99 ? "var(--warn)" : "var(--down)");
  return (
    <div ref={ref} className="mt-2.5 flex h-8 gap-[2px]" role="img" aria-label={`Daily uptime, last ${shown.length} days`}>
      {shown.map((d) => (
        <span
          key={d.day}
          className="min-w-0 flex-1 rounded-[3px] transition-transform hover:scale-y-110"
          style={{ background: color(d.uptime) }}
          onPointerEnter={(e) => tip.show(e.clientX, e.clientY, { title: fmtDay(d.day), rows: [{ label: "Uptime", value: d.uptime == null ? "no data" : fmtUptime(d.uptime), color: color(d.uptime) }] })}
          onPointerLeave={tip.hide}
        />
      ))}
    </div>
  );
}
