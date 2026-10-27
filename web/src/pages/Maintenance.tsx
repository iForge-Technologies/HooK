import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState, type FormEvent } from "react";
import { Modal, useToast } from "../components/overlay";
import { StatusIcon } from "../components/status";
import { Button, Card, Chip, Empty, ErrorNote, Field, Input, Select, Spinner, Toggle } from "../components/ui";
import { api, type Maintenance, type MonitorListItem, type Server } from "../lib/api";
import { fmtDuration } from "../lib/format";
import { useNow } from "../lib/hooks";

const toLocalInput = (ms: number) => {
  const d = new Date(ms);
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
};
const fmtWhen = (ms: number) => new Date(ms).toLocaleString([], { weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

export function MaintenancePage() {
  const q = useQuery({ queryKey: ["maintenance"], queryFn: () => api.get<Maintenance[]>("/maintenance"), refetchInterval: 30_000 });
  const monitors = useQuery({ queryKey: ["monitors"], queryFn: () => api.get<MonitorListItem[]>("/monitors") });
  const servers = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers") });
  const [editing, setEditing] = useState<Partial<Maintenance> | null>(null);
  const now = useNow(30_000);

  const groups = useMemo(() => {
    const ws = q.data ?? [];
    return {
      active: ws.filter((w) => w.active),
      upcoming: ws.filter((w) => !w.active && (w.nextStart ?? 0) > now).sort((a, b) => a.nextStart! - b.nextStart!),
      past: ws.filter((w) => !w.active && !w.nextStart),
    };
  }, [q.data, now]);

  const scope = (w: Maintenance) => {
    if (w.everything) return "Everything";
    const n = w.monitorIds.length, h = w.hostIds.length;
    return [n && `${n} monitor${n > 1 ? "s" : ""}`, h && `${h} server${h > 1 ? "s" : ""}`].filter(Boolean).join(" · ");
  };

  const start = () => {
    const s = Math.ceil(Date.now() / 900_000) * 900_000 + 900_000; // next quarter hour + 15 min
    setEditing({ title: "", startsAt: s, endsAt: s + 3_600_000, repeat: "", everything: false, monitorIds: [], hostIds: [] });
  };

  const Row = ({ w }: { w: Maintenance }) => (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-1.5 border-t border-line px-5 py-3.5 first:border-t-0 sm:px-6">
      <StatusIcon status={w.active ? "maintenance" : w.nextStart ? "pending" : "paused"} className="size-4" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-semibold">{w.title}</div>
        <div className="text-xs text-fg-2">
          {w.nextStart ? (
            <>
              {fmtWhen(w.nextStart)} → {new Date(w.nextEnd!).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
              {w.active ? ` · ends in ${fmtDuration(w.nextEnd! - now)}` : ` · starts in ${fmtDuration(w.nextStart - now)}`}
            </>
          ) : (
            <>Ended {fmtWhen(w.endsAt)}</>
          )}
        </div>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {w.repeat && <Chip>repeats {w.repeat}</Chip>}
        <Chip>{scope(w)}</Chip>
      </div>
      <Button size="sm" onClick={() => setEditing(w)}>
        Edit
      </Button>
    </li>
  );

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-[28px] font-bold tracking-[-0.03em]">Maintenance</h1>
          <p className="mt-0.5 text-sm text-fg-2">During a window, covered monitors show "Maintenance", alerts are muted, and the time doesn't count against uptime.</p>
        </div>
        <Button variant="primary" onClick={start}>
          + Schedule window
        </Button>
      </div>
      {q.isPending ? (
        <div className="grid h-40 place-items-center">
          <Spinner className="size-5" />
        </div>
      ) : (q.data ?? []).length === 0 ? (
        <Card>
          <Empty title="No maintenance scheduled" action={<Button variant="primary" onClick={start}>Schedule window</Button>}>
            Plan deploys, migrations or provider maintenance so Hook doesn't page you for expected downtime. Windows can repeat daily or weekly.
          </Empty>
        </Card>
      ) : (
        (
          [
            ["In progress", groups.active],
            ["Upcoming", groups.upcoming],
            ["Past", groups.past],
          ] as const
        )
          .filter(([, ws]) => ws.length)
          .map(([label, ws]) => (
            <Card key={label} flush className="enter">
              <h2 className="px-5 pb-1 pt-4 text-[13px] font-semibold uppercase tracking-[0.05em] text-fg-2 sm:px-6">{label}</h2>
              <ul>
                {ws.map((w) => (
                  <Row key={w.id} w={w} />
                ))}
              </ul>
            </Card>
          ))
      )}
      {editing && <WindowModal draft={editing} monitors={monitors.data ?? []} servers={servers.data ?? []} onClose={() => setEditing(null)} />}
    </div>
  );
}

function WindowModal({ draft, monitors, servers, onClose }: { draft: Partial<Maintenance>; monitors: MonitorListItem[]; servers: Server[]; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [w, setW] = useState(draft);
  const [error, setError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [filter, setFilter] = useState("");

  const done = (msg: string) => {
    toast(msg);
    qc.invalidateQueries({ queryKey: ["maintenance"] });
    qc.invalidateQueries({ queryKey: ["monitors"] });
    onClose();
  };
  const save = useMutation({
    mutationFn: () => (w.id ? api.put(`/maintenance/${w.id}`, w) : api.post("/maintenance", w)),
    onSuccess: () => done(w.id ? "Window saved" : "Maintenance scheduled"),
    onError: (e) => setError((e as Error).message),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/maintenance/${w.id}`),
    onSuccess: () => done("Window deleted"),
    onError: (e) => setError((e as Error).message),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    save.mutate();
  };
  const toggle = (key: "monitorIds" | "hostIds", id: number) => {
    const cur = w[key] ?? [];
    setW({ ...w, [key]: cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id] });
  };
  const f = filter.toLowerCase();

  return (
    <Modal open onClose={onClose} title={w.id ? "Edit maintenance" : "Schedule maintenance"} width="max-w-2xl">
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Title" htmlFor="mw-title" hint="Shown on monitors and on public status pages.">
          <Input id="mw-title" value={w.title ?? ""} onChange={(e) => setW({ ...w, title: e.target.value })} placeholder="Database upgrade" maxLength={100} required />
        </Field>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="Starts" htmlFor="mw-start">
            <Input id="mw-start" type="datetime-local" value={toLocalInput(w.startsAt!)} onChange={(e) => e.target.value && setW({ ...w, startsAt: new Date(e.target.value).getTime() })} required />
          </Field>
          <Field label="Ends" htmlFor="mw-end">
            <Input id="mw-end" type="datetime-local" value={toLocalInput(w.endsAt!)} onChange={(e) => e.target.value && setW({ ...w, endsAt: new Date(e.target.value).getTime() })} required />
          </Field>
          <Field label="Repeat" htmlFor="mw-repeat" hint="Every 24 h or 7 days from the first start.">
            <Select id="mw-repeat" value={w.repeat ?? ""} onChange={(e) => setW({ ...w, repeat: e.target.value as Maintenance["repeat"] })}>
              <option value="">Once</option>
              <option value="daily">Daily</option>
              <option value="weekly">Weekly</option>
            </Select>
          </Field>
        </div>
        <div className="rounded-2xl bg-surface-2 p-4">
          <Toggle checked={!!w.everything} onChange={(v) => setW({ ...w, everything: v })} label="Everything" hint="All monitors and servers, including ones you add later" />
        </div>
        {!w.everything && (
          <div className="flex flex-col gap-3">
            <Input placeholder="Filter monitors and servers" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter" className="h-9 text-[13px]" />
            <div className="grid max-h-64 gap-1.5 overflow-y-auto pr-1 sm:grid-cols-2">
              {monitors
                .filter((m) => !f || m.name.toLowerCase().includes(f))
                .map((m) => (
                  <label key={"m" + m.id} className="flex cursor-pointer items-center gap-2.5 rounded-xl px-2 py-1.5 text-sm hover:bg-surface-2">
                    <input type="checkbox" className="size-4 accent-[var(--series)]" checked={(w.monitorIds ?? []).includes(m.id)} onChange={() => toggle("monitorIds", m.id)} />
                    <span className="truncate">{m.name}</span>
                    <span className="ml-auto text-xs text-fg-3">monitor</span>
                  </label>
                ))}
              {servers
                .filter((s) => !f || s.name.toLowerCase().includes(f))
                .map((s) => (
                  <label key={"s" + s.id} className="flex cursor-pointer items-center gap-2.5 rounded-xl px-2 py-1.5 text-sm hover:bg-surface-2">
                    <input type="checkbox" className="size-4 accent-[var(--series)]" checked={(w.hostIds ?? []).includes(s.id)} onChange={() => toggle("hostIds", s.id)} />
                    <span className="truncate">{s.name}</span>
                    <span className="ml-auto text-xs text-fg-3">server</span>
                  </label>
                ))}
            </div>
          </div>
        )}
        <ErrorNote>{error}</ErrorNote>
        <div className="flex flex-wrap justify-end gap-2">
          {w.id &&
            (confirmDelete ? (
              <Button variant="danger" className="mr-auto" loading={del.isPending} onClick={() => del.mutate()}>
                Delete window
              </Button>
            ) : (
              <Button variant="ghost" className="mr-auto text-down hover:text-down" onClick={() => setConfirmDelete(true)}>
                Delete…
              </Button>
            ))}
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" loading={save.isPending}>
            {w.id ? "Save" : "Schedule"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
