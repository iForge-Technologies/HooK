import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "wouter";
import { HeartbeatBar } from "../components/charts";
import { shownStatus, StatusBadge } from "../components/status";
import { Button, Card, Empty, Input, Segmented, Spinner } from "../components/ui";
import { api, type MonitorListItem } from "../lib/api";
import { daysUntil, fmtAgo, fmtMs, fmtUptime, TYPE_LABEL } from "../lib/format";
import { useNow } from "../lib/hooks";

type Filter = "all" | "problems" | "up" | "paused";

export function MonitorsPage() {
  const q = useQuery({ queryKey: ["monitors"], queryFn: () => api.get<MonitorListItem[]>("/monitors"), refetchInterval: 60_000 });
  const [filter, setFilter] = useState<Filter>("all");
  const [search, setSearch] = useState("");
  const now = useNow(5000);

  const list = q.data ?? [];
  const stats = useMemo(() => {
    const active = list.filter((m) => m.active);
    const up = active.filter((m) => m.status === "up" || m.status === "degraded").length;
    const down = active.filter((m) => m.status === "down").length;
    const warn = active.filter((m) => m.status === "pending" || m.status === "degraded").length;
    const lat = active.map((m) => m.avgLatency24h).filter((v): v is number => v != null);
    const upt = active.map((m) => m.uptime24h).filter((v): v is number => v != null);
    const certs = active.filter((m) => m.certExpires > 0 && daysUntil(m.certExpires) <= 14);
    return {
      total: active.length,
      paused: list.length - active.length,
      up,
      down,
      warn,
      avgLat: lat.length ? lat.reduce((a, b) => a + b, 0) / lat.length : null,
      uptime: upt.length ? upt.reduce((a, b) => a + b, 0) / upt.length : null,
      certs,
    };
  }, [list]);

  const groups = useMemo(() => {
    const term = search.trim().toLowerCase();
    const rows = list.filter((m) => {
      if (term && !`${m.name} ${m.target} ${m.group}`.toLowerCase().includes(term)) return false;
      if (filter === "problems") return m.active && m.status !== "up";
      if (filter === "up") return m.active && m.status === "up";
      if (filter === "paused") return !m.active;
      return true;
    });
    // problems float to the top of each group
    const rank = { down: 0, pending: 1, degraded: 2, maintenance: 3, up: 4, paused: 5 } as const;
    rows.sort((a, b) => rank[a.status] - rank[b.status] || a.name.localeCompare(b.name));
    const map = new Map<string, MonitorListItem[]>();
    for (const m of rows) map.set(m.group, [...(map.get(m.group) ?? []), m]);
    return [...map.entries()].sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)));
  }, [list, filter, search]);

  if (q.isPending) {
    return (
      <div className="grid h-64 place-items-center text-fg-2">
        <Spinner className="size-5" />
      </div>
    );
  }

  const tone = stats.total === 0 ? "hero-idle" : stats.down > 0 ? "hero-bad" : "hero-ok";
  const headline = stats.total === 0 ? "No monitors yet" : stats.down > 0 ? `${stats.down} ${stats.down === 1 ? "monitor is" : "monitors are"} down` : stats.warn > 0 ? "Mostly operational" : "All systems operational";

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-[28px] font-bold tracking-[-0.03em]">Monitors</h1>
          <p className="mt-0.5 text-sm text-fg-2">
            {stats.total} active{stats.paused ? ` · ${stats.paused} paused` : ""}
          </p>
        </div>
        <Link href="/monitors/new">
          <Button variant="primary">
            <svg viewBox="0 0 16 16" className="size-3.5" aria-hidden>
              <path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            </svg>
            New monitor
          </Button>
        </Link>
      </div>

      {/* summary bento */}
      <div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-12">
        <section className={`hero-card enter col-span-2 flex min-h-[172px] flex-col justify-between rounded-[24px] p-5 sm:p-6 lg:col-span-4 ${tone}`} aria-label="Overall status">
          <div className="text-[15px] font-semibold tracking-[-0.02em]">{headline}</div>
          <div className="flex items-end justify-between gap-4">
            <div>
              <div className="tnum text-[52px] leading-none font-extralight tracking-[-0.05em]">
                {stats.up}
                <span className="text-[26px] text-white/55">/{stats.total}</span>
              </div>
              <div className="mt-2 text-xs font-medium text-white/70">monitors up right now</div>
            </div>
            <div className="text-right">
              <div className="tnum font-mono text-xl">{fmtUptime(stats.uptime)}</div>
              <div className="mt-1 text-xs font-medium text-white/70">avg uptime · 24h</div>
            </div>
          </div>
        </section>
        <StatTile label="Down" value={String(stats.down)} tone={stats.down ? "down" : undefined} note={stats.down ? "Needs attention" : "Nothing down"} />
        <StatTile label="Slow or retrying" value={String(stats.warn)} tone={stats.warn ? "warn" : undefined} note="Degraded or pending" />
        <StatTile label="Avg response" value={fmtMs(stats.avgLat)} note="Across monitors · 24h" />
        <StatTile
          label="Certificates"
          value={String(stats.certs.length)}
          tone={stats.certs.length ? "warn" : undefined}
          note={stats.certs.length ? `Expiring ≤ 14 days` : "None expiring soon"}
        />
      </div>

      {list.length === 0 ? (
        <Card>
          <Empty
            title="Add your first monitor"
            action={
              <Link href="/monitors/new">
                <Button variant="primary">New monitor</Button>
              </Link>
            }
          >
            Watch a website, API, TCP port, DNS record or TLS certificate. Checks start as soon as you save.
          </Empty>
        </Card>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <Segmented
              label="Filter monitors"
              value={filter}
              onChange={setFilter}
              items={[
                { value: "all", label: "All" },
                { value: "problems", label: `Problems${stats.down + stats.warn ? ` · ${stats.down + stats.warn}` : ""}` },
                { value: "up", label: "Up" },
                { value: "paused", label: "Paused" },
              ]}
            />
            <Input type="search" placeholder="Search name, URL or group" value={search} onChange={(e) => setSearch(e.target.value)} className="h-8 max-w-xs flex-1 rounded-[11px] text-[13px]" aria-label="Search monitors" />
          </div>

          {groups.length === 0 && <p className="py-10 text-center text-sm text-fg-2">No monitors match.</p>}
          {groups.map(([group, rows]) => (
            <Card key={group || "_"} flush className="enter">
              <div className="flex items-center justify-between px-5 pb-1 pt-4 sm:px-6">
                <h2 className="text-[13px] font-semibold uppercase tracking-[0.05em] text-fg-2">{group || "Ungrouped"}</h2>
                <span className="text-xs text-fg-3">{rows.length}</span>
              </div>
              <ul>
                {rows.map((m) => (
                  <MonitorRow key={m.id} m={m} now={now} />
                ))}
              </ul>
            </Card>
          ))}
        </>
      )}
    </div>
  );
}

function StatTile({ label, value, note, tone }: { label: string; value: string; note: string; tone?: "down" | "warn" }) {
  return (
    <section className="enter flex min-h-[120px] flex-col justify-between rounded-[24px] border border-line bg-surface p-4 sm:p-5 lg:col-span-2">
      <div className="flex items-center gap-2 text-[11.5px] font-semibold uppercase tracking-[0.06em] text-fg-2">
        {tone && <span className={`size-1.5 rounded-full ${tone === "down" ? "bg-down" : "bg-warn"}`} aria-hidden />}
        {label}
      </div>
      <div>
        <div className="tnum text-[32px] leading-none font-light tracking-[-0.04em]">{value}</div>
        <div className="mt-1.5 text-xs text-fg-3">{note}</div>
      </div>
    </section>
  );
}

function MonitorRow({ m, now }: { m: MonitorListItem; now: number }) {
  const status = shownStatus(m);
  return (
    <li className="border-t border-line first:border-t-0">
      <Link href={`/monitors/${m.id}`} className="group grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 px-5 py-3.5 transition-colors hover:bg-surface-2/60 sm:px-6 md:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_76px_76px_14px]">
        <div className="col-start-1 row-start-1 flex min-w-0 items-center gap-3">
          <div className="w-[92px] shrink-0">
            <StatusBadge status={status} pulse />
          </div>
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold tracking-[-0.01em]">{m.name}</div>
            <div className="flex min-w-0 items-center gap-1.5 text-xs text-fg-3">
              <span className="shrink-0">{TYPE_LABEL[m.type]}</span>
              <span aria-hidden>·</span>
              <span className="truncate font-mono">{m.target}</span>
            </div>
          </div>
        </div>
        <HeartbeatBar beats={m.beats} height={22} className="col-span-2 md:col-span-1" />
        <div className="hidden text-right md:block">
          <div className="tnum font-mono text-sm">{fmtUptime(m.uptime24h)}</div>
          <div className="text-[11px] text-fg-3">24h</div>
        </div>
        <div className="col-start-2 row-start-1 text-right md:col-start-4">
          <div className="tnum font-mono text-sm">{status === "down" || status === "paused" || status === "new" ? "—" : fmtMs(m.lastLatency)}</div>
          <div className="text-[11px] text-fg-3" title="Last check">
            {m.active ? fmtAgo(m.lastCheck, now) : "paused"}
          </div>
        </div>
        <svg viewBox="0 0 16 16" className="hidden size-3.5 text-fg-3 transition-transform group-hover:translate-x-0.5 md:block" aria-hidden>
          <path d="M6 3.5L10.5 8 6 12.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </Link>
    </li>
  );
}
