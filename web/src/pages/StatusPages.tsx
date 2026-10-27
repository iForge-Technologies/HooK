import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Modal, useToast } from "../components/overlay";
import { Button, Card, Empty, ErrorNote, Field, Input, Spinner, Textarea, Toggle } from "../components/ui";
import { api, type Meta, type MonitorListItem, type StatusPage } from "../lib/api";

const slugify = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);

export function StatusPagesPage() {
  const q = useQuery({ queryKey: ["status-pages"], queryFn: () => api.get<StatusPage[]>("/status-pages") });
  const meta = useQuery({ queryKey: ["meta"], queryFn: () => api.get<Meta>("/meta") });
  const [editing, setEditing] = useState<Partial<StatusPage> | null>(null);
  const base = meta.data?.publicUrl || window.location.origin;
  const blank = { title: "", slug: "", description: "", monitorIds: [], showLatency: false };

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-[28px] font-bold tracking-[-0.03em]">Status pages</h1>
          <p className="mt-0.5 text-sm text-fg-2">Public pages for your users: current status, 90 days of uptime, incidents and planned maintenance. No URLs or internal details are shown.</p>
        </div>
        <Button variant="primary" onClick={() => setEditing(blank)}>
          + New status page
        </Button>
      </div>
      {q.isPending ? (
        <div className="grid h-40 place-items-center">
          <Spinner className="size-5" />
        </div>
      ) : (q.data ?? []).length === 0 ? (
        <Card>
          <Empty title="No status pages yet" action={<Button variant="primary" onClick={() => setEditing(blank)}>Create one</Button>}>
            Pick the monitors your users care about. The page is public at its own address and updates every 30 seconds.
          </Empty>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {q.data!.map((p) => (
            <Card key={p.id} className="enter flex flex-col">
              <div className="text-[15px] font-semibold tracking-[-0.02em]">{p.title}</div>
              <a href={`/status/${p.slug}`} target="_blank" rel="noreferrer" className="mt-0.5 truncate font-mono text-xs text-series hover:underline">
                {base}/status/{p.slug} ↗
              </a>
              {p.description && <p className="mt-2 line-clamp-2 text-sm text-fg-2">{p.description}</p>}
              <div className="mt-4 flex items-center justify-between gap-2">
                <span className="text-xs text-fg-3">
                  {p.monitorIds.length} monitor{p.monitorIds.length > 1 ? "s" : ""}
                  {p.showLatency && " · response times shown"}
                </span>
                <Button size="sm" onClick={() => setEditing(p)}>
                  Edit
                </Button>
              </div>
            </Card>
          ))}
        </div>
      )}
      {editing && <PageModal draft={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function PageModal({ draft, onClose }: { draft: Partial<StatusPage>; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const monitors = useQuery({ queryKey: ["monitors"], queryFn: () => api.get<MonitorListItem[]>("/monitors") });
  const [p, setP] = useState(draft);
  const [slugTouched, setSlugTouched] = useState(!!draft.id);
  const [error, setError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);

  const done = (msg: string) => {
    toast(msg);
    qc.invalidateQueries({ queryKey: ["status-pages"] });
    onClose();
  };
  const save = useMutation({
    mutationFn: () => (p.id ? api.put(`/status-pages/${p.id}`, p) : api.post("/status-pages", p)),
    onSuccess: () => done(p.id ? "Status page saved" : "Status page created"),
    onError: (e) => setError((e as Error).message),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/status-pages/${p.id}`),
    onSuccess: () => done("Status page deleted"),
    onError: (e) => setError((e as Error).message),
  });
  const ids = p.monitorIds ?? [];
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    save.mutate();
  };

  return (
    <Modal open onClose={onClose} title={p.id ? "Edit status page" : "New status page"} width="max-w-2xl">
      <form onSubmit={submit} className="flex flex-col gap-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Title" htmlFor="sp-title">
            <Input id="sp-title" value={p.title ?? ""} onChange={(e) => setP({ ...p, title: e.target.value, slug: slugTouched ? p.slug : slugify(e.target.value) })} placeholder="Acme status" maxLength={80} required />
          </Field>
          <Field label="Address" htmlFor="sp-slug" hint={`/status/${p.slug || "…"}`}>
            <Input
              id="sp-slug"
              value={p.slug ?? ""}
              onChange={(e) => {
                setSlugTouched(true);
                setP({ ...p, slug: e.target.value.toLowerCase() });
              }}
              placeholder="acme"
              pattern="[a-z0-9][a-z0-9-]{1,39}"
              className="font-mono text-[13px]"
              required
            />
          </Field>
        </div>
        <Field label="Description (optional)" htmlFor="sp-desc">
          <Textarea id="sp-desc" value={p.description ?? ""} onChange={(e) => setP({ ...p, description: e.target.value })} maxLength={500} className="min-h-16 font-sans text-sm" placeholder="Live status of the Acme app and API." />
        </Field>
        <Toggle checked={!!p.showLatency} onChange={(v) => setP({ ...p, showLatency: v })} label="Show response times" hint="Average over the last 24 hours" />
        <div>
          <div className="mb-2 text-[13px] font-medium text-fg-2">Monitors to show (in this order)</div>
          <div className="grid max-h-64 gap-1.5 overflow-y-auto pr-1 sm:grid-cols-2">
            {(monitors.data ?? []).map((m) => {
              const on = ids.includes(m.id);
              return (
                <label key={m.id} className={`flex cursor-pointer items-center gap-2.5 rounded-xl border px-2.5 py-2 text-sm transition ${on ? "border-series bg-series/10" : "border-line hover:bg-surface-2"}`}>
                  <input type="checkbox" className="size-4 accent-[var(--series)]" checked={on} onChange={() => setP({ ...p, monitorIds: on ? ids.filter((x) => x !== m.id) : [...ids, m.id] })} />
                  <span className="min-w-0 flex-1 truncate">{m.name}</span>
                  {on && <span className="font-mono text-xs text-fg-3">#{ids.indexOf(m.id) + 1}</span>}
                </label>
              );
            })}
          </div>
          <p className="mt-2 text-xs text-fg-3">Visitors see monitor names and groups only. Rename monitors if their names are internal.</p>
        </div>
        <ErrorNote>{error}</ErrorNote>
        <div className="flex flex-wrap justify-end gap-2">
          {p.id &&
            (confirmDelete ? (
              <Button variant="danger" className="mr-auto" loading={del.isPending} onClick={() => del.mutate()}>
                Delete page
              </Button>
            ) : (
              <Button variant="ghost" className="mr-auto text-down hover:text-down" onClick={() => setConfirmDelete(true)}>
                Delete…
              </Button>
            ))}
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" loading={save.isPending}>
            {p.id ? "Save" : "Create page"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
