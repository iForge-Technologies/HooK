import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { Beat, MonitorDetail, MonitorListItem, Series, Status } from "./api";
import { applyHostEvent } from "./live-servers";

type BeatEvent = { monitorId: number; beat: Beat; status: Status; statusSince: number; certExpires: number; certIssuer: string };

/** Subscribes to the hub's event stream and patches cached queries in place,
 *  so lists and charts move without refetching. Returns whether it's connected. */
export function useLiveUpdates(enabled: boolean) {
  const qc = useQueryClient();
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!enabled) return;
    let es: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let firstOpen = true;

    const connect = () => {
      es = new EventSource("/api/events");
      es.onopen = () => {
        setConnected(true);
        // after a reconnect we may have missed beats: resync once
        if (!firstOpen) {
          qc.invalidateQueries({ queryKey: ["monitors"] });
          qc.invalidateQueries({ queryKey: ["servers"] });
        }
        firstOpen = false;
      };
      es.onerror = () => {
        setConnected(false);
        if (es?.readyState === EventSource.CLOSED) {
          // closed for good (e.g. 401): let the auth check decide what to show
          qc.invalidateQueries({ queryKey: ["auth"] });
          retry = setTimeout(connect, 5000);
        }
      };
      es.addEventListener("beat", (e) => {
        const d: BeatEvent = JSON.parse((e as MessageEvent).data);
        const patch = <T extends { status: Status }>(m: T): T => ({
          ...m,
          status: d.status,
          statusSince: d.statusSince,
          lastCheck: d.beat.ts,
          lastLatency: d.beat.latency,
          lastMsg: d.beat.msg ?? "",
          certExpires: d.certExpires,
          certIssuer: d.certIssuer,
        });
        qc.setQueryData<MonitorListItem[]>(["monitors"], (list) =>
          list?.map((m) => (m.id === d.monitorId ? { ...patch(m), beats: [...m.beats, { ...d.beat, timings: undefined }].slice(-60) } : m)),
        );
        qc.setQueryData<MonitorDetail>(["monitor", d.monitorId], (det) =>
          det ? { ...det, monitor: patch(det.monitor), beats: [...det.beats, d.beat].slice(-100) } : det,
        );
        qc.setQueriesData<Series>({ queryKey: ["series", d.monitorId] }, (s) => {
          if (!s || s.resolution !== "raw") return s;
          const cutoff = Date.now() - (s.range === "1h" ? 3600e3 : 86400e3);
          const p = { ts: d.beat.ts, latency: d.beat.latency, status: d.beat.status, msg: d.beat.status !== "up" ? d.beat.msg : undefined };
          return { ...s, points: [...s.points.filter((x) => x.ts >= cutoff), p] };
        });
      });
      for (const t of ["host", "hostSnapshot", "hosts"]) {
        es.addEventListener(t, (e) => applyHostEvent(qc, t, (e as MessageEvent).data));
      }
      es.addEventListener("monitors", () => {
        qc.invalidateQueries({ queryKey: ["monitors"] });
        qc.invalidateQueries({ queryKey: ["monitor"] });
      });
    };
    connect();
    return () => {
      clearTimeout(retry);
      es?.close();
      setConnected(false);
    };
  }, [enabled, qc]);

  return connected;
}
