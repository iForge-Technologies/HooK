import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "wouter";
import { Modal, useToast } from "../components/overlay";
import { StatusIcon } from "../components/status";
import { Button, Card, Chip, Empty, ErrorNote, Field, Input, Spinner } from "../components/ui";
import { api, MASK, type Channel, type Integration, type MonitorListItem, type Project, type Status } from "../lib/api";
import { fmtAgo } from "../lib/format";
import { useNow } from "../lib/hooks";

const KIND = {
  vercel: {
    label: "Vercel",
    tokenHelp: (
      <>
        Create a token at vercel.com/account/tokens. Scope it to the team you want to watch. Hook only reads projects and deployments.
      </>
    ),
  },
  render: {
    label: "Render",
    tokenHelp: <>Create an API key under Account Settings → API Keys on dashboard.render.com. Hook only reads services and deploys.</>,
  },
} as const;

const STATE: Record<Project["state"], { label: string; status: Status }> = {
  ready: { label: "Live", status: "up" },
  building: { label: "Deploying", status: "pending" },
  failed: { label: "Deploy failed", status: "down" },
  canceled: { label: "Canceled", status: "degraded" },
  none: { label: "No deploys", status: "paused" },
  suspended: { label: "Suspended", status: "down" },
};

export function PlatformsPage() {
  const q = useQuery({ queryKey: ["integrations"], queryFn: () => api.get<Integration[]>("/integrations") });
  const [editing, setEditing] = useState<Partial<Integration> | null>(null);

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-[28px] font-bold tracking-[-0.03em]">Platforms</h1>
          <p className="mt-0.5 text-sm text-fg-2">Watch deploys on Vercel and Render, and add uptime checks for their URLs in one click.</p>
        </div>
        <div className="flex gap-2">
          <Button onClick={() => setEditing({ kind: "vercel", name: "Vercel", config: {} })}>+ Vercel</Button>
          <Button onClick={() => setEditing({ kind: "render", name: "Render", config: {} })}>+ Render</Button>
        </div>
      </div>

      {q.isPending ? (
        <div className="grid h-40 place-items-center">
          <Spinner className="size-5" />
        </div>
      ) : q.data?.length === 0 ? (
        <Card>
          <Empty title="Connect a platform account" action={<Button variant="primary" onClick={() => setEditing({ kind: "vercel", name: "Vercel", config: {} })}>Connect Vercel</Button>}>
            Hook lists your projects and services with their latest production deploy, alerts when a deploy fails or a service is suspended, and adds uptime checks for their URLs.
          </Empty>
        </Card>
      ) : (
        q.data?.map((a) => <AccountCard key={a.id} account={a} onEdit={() => setEditing(a)} />)
      )}

      {editing && <AccountModal draft={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function AccountCard({ account, onEdit }: { account: Integration; onEdit: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const now = useNow(30_000);
  const projects = useQuery({ queryKey: ["projects", account.id], queryFn: () => api.get<Project[]>(`/integrations/${account.id}/projects`), refetchInterval: 120_000 });
  const monitors = useQuery({ queryKey: ["monitors"], queryFn: () => api.get<MonitorListItem[]>("/monitors") });
  const channels = useQuery({ queryKey: ["channels"], queryFn: () => api.get<Channel[]>("/channels") });
  const defaults = (channels.data ?? []).filter((c) => c.isDefault && c.active).map((c) => c.id);

  const create = useMutation({
    mutationFn: (body: object) => api.post<{ id: number }>("/monitors", { channelIds: defaults, retries: 1, ...body }),
    onSuccess: () => {
      toast("Monitor added");
      qc.invalidateQueries({ queryKey: ["monitors"] });
    },
    onError: (e) => toast((e as Error).message, "error"),
  });

  const watchDeploys = (p: Project) =>
    create.mutate({ name: `${p.name} deploys`, type: account.kind, group: KIND[account.kind].label, interval: 300, retryInterval: 300, timeout: 30, config: { integrationId: account.id, projectId: p.id, projectName: p.name } });
  // Render free instances sleep after 15 idle minutes, and any request wakes
  // them for another 15. Checking more often than that keeps them awake for
  // good (burning the shared free hours), so free services get an hourly check
  // with a timeout long enough for a cold start.
  const watchUptime = (p: Project) =>
    create.mutate({
      name: p.name,
      type: "http",
      target: p.url,
      group: KIND[account.kind].label,
      interval: p.freePlan ? 3600 : 60,
      retryInterval: p.freePlan ? 120 : 30,
      timeout: p.freePlan ? 90 : 30,
      config: { method: "GET", expectedStatus: "200-399", followRedirects: true, certAlerts: true, ignoreTls: false, keywordInvert: false },
    });

  const ms = monitors.data ?? [];
  const deployMonitor = (p: Project) => ms.find((m) => m.type === account.kind && m.config.projectId === p.id);
  const urlMonitor = (p: Project) => p.url && ms.find((m) => (m.type === "http" || m.type === "keyword") && m.target.replace(/\/$/, "") === p.url.replace(/\/$/, ""));

  return (
    <Card className="enter" flush>
      <div className="flex flex-wrap items-center justify-between gap-3 px-5 pt-5 sm:px-6">
        <div className="flex items-center gap-3">
          <span className="grid size-9 place-items-center rounded-xl bg-fg text-bg">
            {account.kind === "vercel" ? (
              <svg viewBox="0 0 24 24" className="size-4" aria-hidden>
                <path d="M12 3l10 18H2z" fill="currentColor" />
              </svg>
            ) : (
              <span className="text-sm font-bold">R</span>
            )}
          </span>
          <div>
            <div className="text-[15px] font-semibold tracking-[-0.02em]">{account.name}</div>
            <div className="text-xs text-fg-3">
              {KIND[account.kind].label}
              {account.config.teamId && ` · team ${account.config.teamId}`}
              {projects.data && ` · ${projects.data.length} ${account.kind === "vercel" ? "projects" : "services"}`}
            </div>
          </div>
        </div>
        <div className="flex gap-2">
          <Button size="sm" variant="ghost" onClick={() => qc.invalidateQueries({ queryKey: ["projects", account.id] })} loading={projects.isFetching}>
            Refresh
          </Button>
          <Button size="sm" onClick={onEdit}>
            Edit
          </Button>
        </div>
      </div>
      {projects.isPending ? (
        <div className="grid h-32 place-items-center">
          <Spinner />
        </div>
      ) : projects.isError ? (
        <div className="p-5 sm:p-6">
          <ErrorNote>{(projects.error as Error).message}</ErrorNote>
        </div>
      ) : (
        <ul className="mt-3">
          {projects.data!.map((p) => {
            const st = STATE[p.state] ?? STATE.none;
            const dm = deployMonitor(p), um = urlMonitor(p);
            return (
              <li key={p.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 border-t border-line px-5 py-3.5 sm:px-6 md:grid-cols-[minmax(0,1.2fr)_150px_minmax(0,1fr)_auto]">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm font-semibold">{p.name}</span>
                    {p.freePlan && <Chip className="h-5 px-1.5 text-[11px]">free · sleeps</Chip>}
                  </div>
                  <div className="truncate font-mono text-xs text-fg-3">{p.url || p.kind || "no public URL"}</div>
                </div>
                <div className="row-start-1 flex items-center gap-1.5 text-xs md:row-start-auto" title={p.detail}>
                  <StatusIcon status={st.status} className="size-3.5" />
                  <span className="font-medium">{st.label}</span>
                  {p.updatedAt > 0 && <span className="text-fg-3">· {fmtAgo(p.updatedAt, now)}</span>}
                </div>
                <div className="col-span-2 flex flex-wrap gap-2 md:col-span-1">
                  {dm ? (
                    <Link href={`/monitors/${dm.id}`}>
                      <Chip className="hover:text-fg">Deploys watched →</Chip>
                    </Link>
                  ) : (
                    <Button size="sm" onClick={() => watchDeploys(p)} disabled={create.isPending}>
                      Watch deploys
                    </Button>
                  )}
                  {p.url &&
                    (um ? (
                      <Link href={`/monitors/${um.id}`}>
                        <Chip className="hover:text-fg">Uptime watched →</Chip>
                      </Link>
                    ) : (
                      <Button size="sm" onClick={() => watchUptime(p)} disabled={create.isPending} title={p.freePlan ? "Free plan: checks hourly with a 90 s timeout, so the instance can sleep between checks and wake for each one" : undefined}>
                        Monitor uptime
                      </Button>
                    ))}
                </div>
                {p.dashboard ? (
                  <a href={p.dashboard} target="_blank" rel="noreferrer noopener" className="hidden text-xs text-fg-3 hover:text-fg md:block">
                    Open ↗
                  </a>
                ) : (
                  <span className="hidden md:block" />
                )}
              </li>
            );
          })}
          {projects.data!.length === 0 && <li className="border-t border-line px-6 py-6 text-sm text-fg-2">No projects visible to this token.</li>}
        </ul>
      )}
      <p className="border-t border-line px-5 py-3 text-xs text-fg-3 sm:px-6">
        "Deploy failed" means the latest production deploy failed; the platform usually keeps serving the previous one. Use "Monitor uptime" to know whether the site itself is up.
      </p>
    </Card>
  );
}

function AccountModal({ draft, onClose }: { draft: Partial<Integration>; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState(draft.name ?? "");
  const [token, setToken] = useState(draft.config?.token ?? "");
  const [teamId, setTeamId] = useState(draft.config?.teamId ?? "");
  const [error, setError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const kind = draft.kind as Integration["kind"];

  const save = useMutation({
    mutationFn: () => {
      const body = { kind, name, config: { token, teamId } };
      return draft.id ? api.put<{ projects: number }>(`/integrations/${draft.id}`, body) : api.post<{ projects: number }>("/integrations", body);
    },
    onSuccess: (r) => {
      toast(`Connected · ${r.projects} ${kind === "vercel" ? "projects" : "services"} found`);
      qc.invalidateQueries({ queryKey: ["integrations"] });
      qc.invalidateQueries({ queryKey: ["projects"] });
      onClose();
    },
    onError: (e) => setError((e as Error).message),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/integrations/${draft.id}`),
    onSuccess: () => {
      toast("Account removed");
      qc.invalidateQueries({ queryKey: ["integrations"] });
      onClose();
    },
    onError: (e) => setError((e as Error).message),
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    save.mutate();
  };

  return (
    <Modal open onClose={onClose} title={draft.id ? `Edit ${draft.name}` : `Connect ${KIND[kind].label}`}>
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Name" htmlFor="pa-name">
          <Input id="pa-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} required />
        </Field>
        <Field label="API token" htmlFor="pa-token" hint={KIND[kind].tokenHelp}>
          <Input
            id="pa-token"
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            onFocus={(e) => e.target.value === MASK && setToken("")}
            autoComplete="off"
            className="font-mono text-[13px]"
            required
          />
        </Field>
        {kind === "vercel" && (
          <Field label="Team ID (optional)" htmlFor="pa-team" hint="Needed for team projects: team_… from the team's Settings → General.">
            <Input id="pa-team" value={teamId} onChange={(e) => setTeamId(e.target.value)} placeholder="team_…" className="font-mono text-[13px]" />
          </Field>
        )}
        <p className="text-xs text-fg-3">The token is checked against the API before saving, stored encrypted, and never shown again.</p>
        <ErrorNote>{error}</ErrorNote>
        <div className="flex flex-wrap justify-end gap-2">
          {draft.id &&
            (confirmDelete ? (
              <Button variant="danger" className="mr-auto" loading={del.isPending} onClick={() => del.mutate()}>
                Remove account
              </Button>
            ) : (
              <Button variant="ghost" className="mr-auto text-down hover:text-down" onClick={() => setConfirmDelete(true)}>
                Remove…
              </Button>
            ))}
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" loading={save.isPending}>
            {draft.id ? "Save" : "Connect"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
