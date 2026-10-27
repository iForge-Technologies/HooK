import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { STATUS, StatusIcon } from "../components/status";
import { DotMark, Spinner } from "../components/ui";
import { api, ApiError, type Status } from "../lib/api";
import { fmtDuration, fmtMs, fmtUptime } from "../lib/format";
import { useNow } from "../lib/hooks";

interface KioskMonitor {
  name: string;
  group: string;
  status: Status;
  since: number;
  latency?: number;
  uptime24h?: number;
  beats: Status[];
}

interface KioskServer {
  name: string;
  group: string;
  status: "new" | "online" | "offline";
  since: number;
  cpu?: number;
  mem?: number;
  disk?: number;
  open: string[];
  connected: boolean;
}

interface Board {
  name: string;
  overall: "up" | "degraded" | "down";
  updatedAt: number;
  monitors: KioskMonitor[];
  servers: KioskServer[];
}

const OPEN_LABEL: Record<string, string> = { cpu: "CPU high", mem: "Memory high", disk: "Disk filling", container: "Container down", service: "Service down" };

const rank = (s: Status) => ({ down: 0, pending: 1, degraded: 1, maintenance: 2, up: 3, paused: 4 })[s] ?? 3;

/**
 * Full-screen wall display opened from a kiosk link. No session: the token in
 * the URL is the credential, and the API returns names and states only.
 */
export function KioskPage({ token }: { token: string }) {
  const q = useQuery({
    queryKey: ["kiosk", token],
    queryFn: () => api.get<Board>(`/kiosk/${encodeURIComponent(token)}`),
    refetchInterval: 10_000,
    refetchIntervalInBackground: true,
    retry: (n, err) => !(err instanceof ApiError && err.status === 404) && n < 3,
  });
  const now = useNow(1000);
  const idle = useIdle(3000);
  useKioskSetup();
  useWakeLock();

  useEffect(() => {
    if (q.data) document.title = `${q.data.overall === "up" ? "●" : "▲"} ${q.data.name} · Hook`;
  }, [q.data]);

  if (q.isPending) {
    return (
      <div className="grid min-h-dvh place-items-center">
        <Spinner className="size-6" />
      </div>
    );
  }
  // A revoked link must blank the screen, not leave the last board frozen on it.
  const gone = q.error instanceof ApiError && q.error.status === 404;
  if (!q.data || gone) {
    return (
      <div className="grid min-h-dvh place-items-center px-6 text-center">
        <div>
          <DotMark className="mx-auto mb-5 size-12" />
          <p className="text-xl font-semibold">{gone ? "This kiosk link was revoked" : "Can't reach the Hook hub"}</p>
          <p className="mt-2 text-fg-2">{gone ? "Create a new one in Settings → Kiosk mode." : "Retrying every 10 seconds."}</p>
        </div>
      </div>
    );
  }

  const b = q.data;
  const stale = now - q.dataUpdatedAt > 35_000;
  const monitors = [...b.monitors].sort((a, c) => rank(a.status) - rank(c.status) || a.name.localeCompare(c.name));
  const servers = [...b.servers].sort((a, c) => Number(c.status === "offline") - Number(a.status === "offline") || c.open.length - a.open.length || a.name.localeCompare(c.name));
  const down = b.monitors.filter((m) => m.status === "down").length + b.servers.filter((s) => s.status === "offline").length;
  const warn = b.monitors.filter((m) => m.status === "pending" || m.status === "degraded").length + b.servers.filter((s) => s.status !== "offline" && s.open.length > 0).length;
  const healthy = b.monitors.filter((m) => m.status === "up").length + b.servers.filter((s) => s.status === "online" && s.open.length === 0).length;
  const total = b.monitors.filter((m) => m.status !== "paused").length + b.servers.filter((s) => s.status !== "new").length;

  const headline = b.overall === "up" ? "All systems operational" : down > 0 ? `${down} ${down === 1 ? "thing is" : "things are"} down` : `${warn} ${warn === 1 ? "warning" : "warnings"}`;
  const overallStatus: Status = b.overall === "up" ? "up" : b.overall === "down" ? "down" : "degraded";

  return (
    <div className={`flex min-h-dvh flex-col gap-6 p-6 lg:p-8 ${idle ? "cursor-none" : ""}`}>
      <header className="flex flex-wrap items-center gap-x-8 gap-y-4">
        <div className="flex min-w-0 basis-full items-center gap-4 md:basis-0 md:flex-1">
          <StatusIcon status={overallStatus} className="size-12 shrink-0" />
          <div className="min-w-0">
            <h1 className="text-3xl font-bold tracking-tight md:truncate lg:text-4xl">{headline}</h1>
            <p className="mt-1 text-fg-2">
              {b.name} · {healthy} of {total} healthy
              {stale && <span className="text-warn"> · not updated for {fmtDuration(now - q.dataUpdatedAt)}, reconnecting…</span>}
            </p>
          </div>
        </div>
        <Clock now={now} />
        <button
          onClick={() => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen()).catch(() => {})}
          className={`ml-auto rounded-xl border border-line px-3 py-2 md:ml-0 text-sm text-fg-2 transition hover:bg-surface-2 ${idle ? "opacity-0" : ""}`}
        >
          Full screen
        </button>
      </header>

      {monitors.length > 0 && (
        <section aria-label="Monitors">
          <h2 className="mb-3 text-sm font-medium tracking-wide text-fg-3 uppercase">Monitors</h2>
          <div className="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(17rem,1fr))]">
            {monitors.map((m, i) => (
              <MonitorTile key={i} m={m} now={now} />
            ))}
          </div>
        </section>
      )}

      {servers.length > 0 && (
        <section aria-label="Servers">
          <h2 className="mb-3 text-sm font-medium tracking-wide text-fg-3 uppercase">Servers</h2>
          <div className="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(19rem,1fr))]">
            {servers.map((s, i) => (
              <ServerTile key={i} s={s} now={now} />
            ))}
          </div>
        </section>
      )}

      {monitors.length + servers.length === 0 && <p className="grid flex-1 place-items-center text-fg-2">Nothing is being monitored yet.</p>}

      <footer className="mt-auto flex items-center gap-2 text-xs text-fg-3">
        <DotMark className="size-4" />
        Hook · refreshes every 10 seconds
      </footer>
    </div>
  );
}

function Clock({ now }: { now: number }) {
  const d = new Date(now);
  // Seconds and AM/PM in smaller type, wherever the locale puts them.
  const parts = new Intl.DateTimeFormat([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }).formatToParts(d);
  return (
    <div className="md:text-right">
      <div className="tnum text-4xl font-semibold tracking-tight lg:text-5xl">
        {parts.map((p, i) => {
          const small = p.type === "second" || p.type === "dayPeriod" || (p.type === "literal" && parts[i + 1]?.type === "second");
          return (
            <span key={i} className={small ? "text-2xl text-fg-3 lg:text-3xl" : ""}>
              {p.value}
            </span>
          );
        })}
      </div>
      <div className="text-sm text-fg-2">{d.toLocaleDateString([], { weekday: "long", month: "long", day: "numeric" })}</div>
    </div>
  );
}

function MonitorTile({ m, now }: { m: KioskMonitor; now: number }) {
  const bad = m.status === "down";
  const warn = m.status === "pending" || m.status === "degraded";
  return (
    <div className={`rounded-2xl border bg-surface p-4 ${bad ? "border-down/70 bg-down/10" : warn ? "border-warn/60" : "border-line"}`}>
      <div className="flex items-center gap-2.5">
        <StatusIcon status={m.status} className="size-5" />
        <span className="min-w-0 flex-1 truncate text-lg font-semibold tracking-tight">{m.name}</span>
      </div>
      <div className="mt-1 flex items-baseline justify-between gap-3 text-sm">
        <span className={STATUS[m.status]?.cls ?? "text-fg-2"}>
          {STATUS[m.status]?.label ?? m.status}
          {m.since > 0 && m.status !== "paused" && <span className="text-fg-3"> for {fmtDuration(now - m.since)}</span>}
        </span>
        <span className="tnum shrink-0 text-fg-2">{fmtMs(m.latency)}</span>
      </div>
      <Beats beats={m.beats} />
      <div className="mt-1.5 text-xs text-fg-3">
        <span className="tnum">{fmtUptime(m.uptime24h)}</span> last 24 h
      </div>
    </div>
  );
}

/** The last 30 checks, oldest first; colour plus height so state isn't colour-only. */
function Beats({ beats }: { beats: Status[] }) {
  const pad = Math.max(0, 30 - beats.length);
  return (
    <div className="mt-3 flex h-6 items-end gap-[3px]" aria-hidden>
      {Array.from({ length: pad }, (_, i) => (
        <span key={"p" + i} className="h-2 flex-1 rounded-full bg-surface-3" />
      ))}
      {beats.map((s, i) => (
        <span key={i} className="flex-1 rounded-full" style={{ height: s === "up" ? "100%" : s === "down" ? "45%" : "70%", background: STATUS[s]?.color ?? "var(--paused)" }} />
      ))}
    </div>
  );
}

function ServerTile({ s, now }: { s: KioskServer; now: number }) {
  const off = s.status === "offline";
  const status: Status = off ? "down" : s.status === "new" ? "pending" : s.open.length > 0 ? "degraded" : "up";
  return (
    <div className={`rounded-2xl border bg-surface p-4 ${off ? "border-down/70 bg-down/10" : s.open.length ? "border-warn/60" : "border-line"}`}>
      <div className="flex items-center gap-2.5">
        <StatusIcon status={status} className="size-5" />
        <span className="min-w-0 flex-1 truncate text-lg font-semibold tracking-tight">{s.name}</span>
      </div>
      <p className={`mt-1 truncate text-sm ${off ? "text-down" : s.open.length ? "text-warn" : "text-fg-2"}`}>
        {off ? `Offline for ${fmtDuration(now - s.since)}` : s.status === "new" ? "Waiting for the agent" : s.open.length ? s.open.map((k) => OPEN_LABEL[k] ?? k).join(" · ") : "Online"}
      </p>
      {!off && s.cpu != null && (
        <div className="mt-3 grid gap-2">
          <Gauge label="CPU" v={s.cpu} />
          <Gauge label="Memory" v={s.mem} />
          <Gauge label="Disk" v={s.disk} />
        </div>
      )}
    </div>
  );
}

function Gauge({ label, v }: { label: string; v?: number }) {
  const pct = Math.max(0, Math.min(100, v ?? 0));
  const hot = pct >= 90;
  return (
    <div className="grid grid-cols-[4.5rem_1fr_3.5rem] items-center gap-3 text-sm">
      <span className="text-fg-2">{label}</span>
      <span className="h-2 overflow-hidden rounded-full bg-surface-3">
        <span className="block h-full rounded-full transition-[width] duration-700" style={{ width: `${pct}%`, background: hot ? "var(--warn)" : "var(--series)" }} />
      </span>
      <span className={`tnum text-right ${hot ? "font-semibold text-warn" : ""}`}>{v == null ? "—" : `${Math.round(pct)}%`}</span>
    </div>
  );
}

/** Dark theme and a root font size that scales with the screen, so a TV across the room is readable. */
function useKioskSetup() {
  useEffect(() => {
    const root = document.documentElement;
    const hadLight = root.classList.contains("light");
    root.classList.remove("light");
    root.style.fontSize = "clamp(14px, 0.95vw, 26px)";
    return () => {
      root.classList.toggle("light", hadLight);
      root.style.fontSize = "";
    };
  }, []);
}

/** Keeps the screen on while the board is visible (where the browser allows it). */
function useWakeLock() {
  useEffect(() => {
    if (!("wakeLock" in navigator)) return;
    let lock: WakeLockSentinel | null = null;
    let done = false;
    const acquire = () => {
      if (document.visibilityState !== "visible") return;
      navigator.wakeLock
        .request("screen")
        .then((l) => {
          if (done) l.release();
          else lock = l;
        })
        .catch(() => {});
    };
    acquire();
    document.addEventListener("visibilitychange", acquire);
    return () => {
      done = true;
      document.removeEventListener("visibilitychange", acquire);
      lock?.release().catch(() => {});
    };
  }, []);
}

/** True after `ms` without pointer movement: hides the cursor and controls. */
function useIdle(ms: number) {
  const [idle, setIdle] = useState(false);
  const timer = useRef(0);
  useEffect(() => {
    const wake = () => {
      setIdle(false);
      clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setIdle(true), ms);
    };
    wake();
    window.addEventListener("pointermove", wake);
    window.addEventListener("keydown", wake);
    return () => {
      clearTimeout(timer.current);
      window.removeEventListener("pointermove", wake);
      window.removeEventListener("keydown", wake);
    };
  }, [ms]);
  return idle;
}
