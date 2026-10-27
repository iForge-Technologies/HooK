import type { QueryClient } from "@tanstack/react-query";
import type { Metrics, Server, ServerDetail, ServerSeries, Snapshot } from "./api";

/** Patches server queries from the hub's "host" and "hostSnapshot" events. */
export function applyHostEvent(qc: QueryClient, type: string, data: string) {
  if (type === "hosts") {
    qc.invalidateQueries({ queryKey: ["servers"] });
    qc.invalidateQueries({ queryKey: ["server"] });
    return;
  }
  if (type === "host") {
    const d: { hostId: number; metrics: Metrics } = JSON.parse(data);
    qc.setQueryData<Server[]>(["servers"], (list) => list?.map((s) => (s.id === d.hostId ? { ...s, metrics: d.metrics, connected: true, lastSeen: d.metrics.ts } : s)));
    qc.setQueryData<ServerDetail>(["server", d.hostId], (det) =>
      det ? { ...det, server: { ...det.server, metrics: d.metrics, connected: true, lastSeen: d.metrics.ts } } : det,
    );
    qc.setQueryData<ServerSeries>(["serverSeries", d.hostId, "1h"], (s) => {
      if (!s || s.resolution !== "raw") return s;
      const cutoff = Date.now() - 3600e3;
      return { ...s, points: [...s.points.filter((p) => p.ts >= cutoff), d.metrics] };
    });
    return;
  }
  if (type === "hostSnapshot") {
    const d: { hostId: number; snapshot: Snapshot } = JSON.parse(data);
    qc.setQueryData<ServerDetail>(["server", d.hostId], (det) => (det ? { ...det, snapshot: d.snapshot } : det));
  }
}
