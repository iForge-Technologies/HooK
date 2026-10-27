import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { Link, useLocation } from "wouter";
import { useToast } from "../components/overlay";
import { Button, Card, CardTitle, ErrorNote, Field, Input, Select, Spinner, Textarea, Toggle } from "../components/ui";
import { api, DB_TYPES, MASK, type Channel, type Integration, type Meta, type MonitorConfig, type MonitorDetail, type MonitorType, type Project } from "../lib/api";
import { fmtInterval } from "../lib/format";

const TYPES: { value: MonitorType; label: string; desc: string }[] = [
  { value: "http", label: "HTTP(S)", desc: "Status code and response time of a URL" },
  { value: "keyword", label: "Keyword", desc: "Page must (or must not) contain some text" },
  { value: "tcp", label: "TCP port", desc: "A port accepts connections (SSH, SMTP…)" },
  { value: "dns", label: "DNS", desc: "A record resolves, optionally to a value" },
  { value: "tls", label: "TLS certificate", desc: "Certificate is valid and not expiring" },
  { value: "push", label: "Push / cron job", desc: "Your job calls a URL; alert if it goes quiet" },
  { value: "postgres", label: "PostgreSQL", desc: "Connects and runs SELECT 1" },
  { value: "mysql", label: "MySQL / MariaDB", desc: "Connects and runs SELECT VERSION()" },
  { value: "redis", label: "Redis", desc: "Authenticates and sends PING" },
  { value: "mongodb", label: "MongoDB", desc: "Connects and pings the primary" },
  { value: "vercel", label: "Vercel deploys", desc: "Latest production deploy succeeded" },
  { value: "render", label: "Render deploys", desc: "Latest deploy is live, service not suspended" },
];

const INTERVALS = [20, 30, 60, 120, 300, 600, 900, 1800, 3600];
const PUSH_INTERVALS = [60, 300, 900, 1800, 3600, 7200, 21600, 43200, 86400, 604800];

const DSN_HINT: Partial<Record<MonitorType, string>> = {
  postgres: "postgres://monitor:pass@host:5432/db?sslmode=require",
  mysql: "mysql://monitor:pass@host:3306/db",
  redis: "rediss://default:pass@host:6380/0",
  mongodb: "mongodb+srv://monitor:pass@cluster.example.net/",
};

const hasTarget = (t: MonitorType) => ["http", "keyword", "tcp", "dns", "tls"].includes(t);
const isPlatform = (t: MonitorType) => t === "vercel" || t === "render";
const METHODS = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"];

type Form = {
  name: string;
  type: MonitorType;
  target: string;
  group: string;
  interval: number;
  retryInterval: number;
  timeout: number;
  retries: number;
  degradedMs: number;
  config: MonitorConfig;
  headers: [string, string][];
  channelIds: number[];
};

const blank = (): Form => ({
  name: "",
  type: "http",
  target: "",
  group: "",
  interval: 60,
  retryInterval: 30,
  timeout: 30,
  retries: 1,
  degradedMs: 0,
  config: { method: "GET", expectedStatus: "200-299", followRedirects: true, ignoreTls: false, keywordInvert: false, certAlerts: true, dnsRecord: "A" },
  headers: [],
  channelIds: [],
});

const PLACEHOLDER: Record<MonitorType, string> = {
  http: "https://example.com/health",
  keyword: "https://example.com",
  tcp: "db.example.com:5432",
  dns: "example.com",
  tls: "example.com or mail.example.com:465",
  postgres: "",
  mysql: "",
  redis: "",
  mongodb: "",
  push: "",
  vercel: "",
  render: "",
};

/** Account + project pickers for Vercel and Render deploy monitors. */
function PlatformPicker({ kind, integrationId, projectId, onChange }: { kind: "vercel" | "render"; integrationId?: number; projectId?: string; onChange: (integrationId: number, projectId: string, projectName: string) => void }) {
  const accounts = useQuery({ queryKey: ["integrations"], queryFn: () => api.get<Integration[]>("/integrations") });
  const mine = (accounts.data ?? []).filter((a) => a.kind === kind);
  const acct = integrationId ?? mine[0]?.id;
  const projects = useQuery({ queryKey: ["projects", acct], queryFn: () => api.get<Project[]>(`/integrations/${acct}/projects`), enabled: !!acct });
  if (accounts.isPending) return <Spinner />;
  if (mine.length === 0) {
    return (
      <p className="text-sm text-fg-2">
        No {kind === "vercel" ? "Vercel" : "Render"} account connected.{" "}
        <Link href="/platforms" className="text-series hover:underline">
          Connect one on Platforms
        </Link>
        , then pick a project here (or use "Watch deploys" there).
      </p>
    );
  }
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field label="Account" htmlFor="pf-acct">
        <Select id="pf-acct" value={acct} onChange={(e) => onChange(Number(e.target.value), "", "")}>
          {mine.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </Select>
      </Field>
      <Field label={kind === "vercel" ? "Project" : "Service"} htmlFor="pf-proj" error={projects.isError ? (projects.error as Error).message : undefined}>
        <Select
          id="pf-proj"
          value={projectId ?? ""}
          required
          onChange={(e) => {
            const p = projects.data?.find((x) => x.id === e.target.value);
            onChange(acct!, e.target.value, p?.name ?? "");
          }}
        >
          <option value="" disabled>
            {projects.isPending ? "Loading…" : "Choose…"}
          </option>
          {projects.data?.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>
      </Field>
    </div>
  );
}

export function MonitorFormPage({ id }: { id?: number }) {
  const editing = id != null;
  const qc = useQueryClient();
  const toast = useToast();
  const [, navigate] = useLocation();
  const meta = useQuery({ queryKey: ["meta"], queryFn: () => api.get<Meta>("/meta") });
  const channels = useQuery({ queryKey: ["channels"], queryFn: () => api.get<Channel[]>("/channels") });
  const existing = useQuery({ queryKey: ["monitor", id], queryFn: () => api.get<MonitorDetail>(`/monitors/${id}`), enabled: editing });

  const [f, setF] = useState<Form | null>(editing ? null : blank());
  const [error, setError] = useState("");

  // Seed the form once from the loaded monitor (edit) or default channels (new).
  useEffect(() => {
    if (editing && existing.data && !f) {
      const m = existing.data.monitor;
      setF({
        name: m.name,
        type: m.type,
        target: m.target,
        group: m.group,
        interval: m.interval,
        retryInterval: m.retryInterval,
        timeout: m.timeout,
        retries: m.retries,
        degradedMs: m.degradedMs,
        config: { ...blank().config, ...m.config },
        headers: Object.entries(m.config.headers ?? {}),
        channelIds: m.channelIds,
      });
    }
  }, [editing, existing.data, f]);
  const [seededChannels, setSeeded] = useState(false);
  useEffect(() => {
    if (!editing && channels.data && !seededChannels) {
      setSeeded(true);
      setF((p) => p && { ...p, channelIds: channels.data.filter((c) => c.isDefault && c.active).map((c) => c.id) });
    }
  }, [editing, channels.data, seededChannels]);

  const save = useMutation({
    mutationFn: async (body: unknown): Promise<number> => {
      if (editing) {
        await api.put(`/monitors/${id}`, body);
        return id;
      }
      return (await api.post<{ id: number }>("/monitors", body)).id;
    },
    onSuccess: (savedId) => {
      qc.invalidateQueries({ queryKey: ["monitors"] });
      qc.invalidateQueries({ queryKey: ["monitor", id] });
      qc.invalidateQueries({ queryKey: ["meta"] });
      toast(editing ? "Monitor saved" : "Monitor created, first check running");
      navigate(`/monitors/${savedId}`);
    },
    onError: (e) => setError((e as Error).message),
  });

  if (!f || (editing && existing.isPending)) {
    return (
      <div className="grid h-64 place-items-center text-fg-2">
        {existing.isError ? <p>{(existing.error as Error).message}</p> : <Spinner className="size-5" />}
      </div>
    );
  }

  const set = <K extends keyof Form>(k: K, v: Form[K]) => setF({ ...f, [k]: v });
  const setC = <K extends keyof MonitorConfig>(k: K, v: MonitorConfig[K]) => setF({ ...f, config: { ...f.config, [k]: v } });
  const isUrl = f.type === "http" || f.type === "keyword";
  const isDB = DB_TYPES.includes(f.type);
  const isPush = f.type === "push";
  const hasBody = isUrl && !["GET", "HEAD"].includes(f.config.method ?? "GET");

  // Switching type nudges the interval to something sensible for it.
  const pickType = (t: MonitorType) => {
    let interval = f.interval;
    if (t === "push" && interval < 300) interval = 3600;
    if (t !== "push" && interval > 3600) interval = 60;
    if (isPlatform(t) && interval < 300) interval = 300;
    setF({ ...f, type: t, interval, timeout: Math.min(f.timeout, interval) });
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    const headers: Record<string, string> = {};
    for (const [k, v] of f.headers) if (k.trim()) headers[k.trim()] = v;
    save.mutate({
      name: f.name,
      type: f.type,
      target: hasTarget(f.type) ? f.target : "",
      group: f.group,
      interval: f.interval,
      retryInterval: f.retryInterval,
      timeout: Math.min(f.timeout, f.interval),
      retries: f.retries,
      degradedMs: f.degradedMs,
      channelIds: f.channelIds,
      config: { ...f.config, headers, body: hasBody ? f.config.body : "" },
    });
  };

  // Suggest a name from the hostname when the user hasn't typed one.
  const autoName = () => {
    const src = isDB ? f.config.dsn ?? "" : f.target;
    if (f.name.trim() || !src.trim() || src === MASK) return;
    try {
      const host = isUrl || isDB ? new URL(src).hostname : src.split(":")[0];
      set("name", (isDB ? `${f.type} ${host}` : host).replace(/^www\./, ""));
    } catch {
      /* not a URL yet */
    }
  };

  const base = isPush ? PUSH_INTERVALS : isPlatform(f.type) ? INTERVALS.filter((s) => s >= 60) : INTERVALS;
  const intervalOptions = base.includes(f.interval) ? base : [...base, f.interval].sort((a, b) => a - b);

  return (
    <form onSubmit={submit} className="mx-auto flex max-w-3xl flex-col gap-4 sm:gap-5">
      <Link href={editing ? `/monitors/${id}` : "/monitors"} className="inline-flex w-fit items-center gap-1 text-sm text-fg-2 hover:text-fg">
        <svg viewBox="0 0 16 16" className="size-3.5" aria-hidden>
          <path d="M10 3.5L5.5 8l4.5 4.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        {editing ? "Back to monitor" : "Monitors"}
      </Link>
      <h1 className="text-[28px] font-bold tracking-[-0.03em]">{editing ? "Edit monitor" : "New monitor"}</h1>

      <Card>
        <CardTitle>What to check</CardTitle>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3" role="radiogroup" aria-label="Monitor type">
          {TYPES.map((t) => (
            <button
              key={t.value}
              type="button"
              role="radio"
              aria-checked={f.type === t.value}
              aria-label={`${t.label}: ${t.desc}`}
              onClick={() => pickType(t.value)}
              className={`rounded-2xl border p-3.5 text-left outline-none transition focus-visible:ring-2 focus-visible:ring-series ${f.type === t.value ? "border-series bg-series/10" : "border-line hover:border-line-strong hover:bg-surface-2"}`}
            >
              <div className="text-sm font-semibold">{t.label}</div>
              <div className="mt-0.5 text-xs leading-snug text-fg-2">{t.desc}</div>
            </button>
          ))}
        </div>

        <div className="mt-5 grid gap-4 sm:grid-cols-2">
          {hasTarget(f.type) && (
            <div className="sm:col-span-2">
              <Field label={isUrl ? "URL" : "Host"} htmlFor="target" hint={f.type === "tls" ? "Port defaults to 443." : undefined}>
                <Input id="target" value={f.target} onChange={(e) => set("target", e.target.value)} onBlur={autoName} placeholder={PLACEHOLDER[f.type]} className="font-mono text-[13px]" required autoFocus={!editing} />
              </Field>
            </div>
          )}
          {isDB && (
            <div className="sm:col-span-2">
              <Field
                label="Connection string"
                htmlFor="dsn"
                hint={
                  <>
                    Use a read-only user; the check never writes. Stored encrypted and never shown again.
                    {f.type === "postgres" && " Key=value form (host=… user=…) works too."}
                    {f.type === "mysql" && " The driver's own user:pass@tcp(host:3306)/db form works too."}
                  </>
                }
              >
                <Input
                  id="dsn"
                  type={f.config.dsn === MASK ? "password" : "text"}
                  value={f.config.dsn ?? ""}
                  onChange={(e) => setC("dsn", e.target.value)}
                  onFocus={(e) => e.target.value === MASK && setC("dsn", "")}
                  onBlur={autoName}
                  placeholder={DSN_HINT[f.type]}
                  autoComplete="off"
                  spellCheck={false}
                  className="font-mono text-[13px]"
                  required
                />
              </Field>
            </div>
          )}
          {(f.type === "vercel" || f.type === "render") && (
            <div className="sm:col-span-2">
              <PlatformPicker
                kind={f.type}
                integrationId={f.config.integrationId}
                projectId={f.config.projectId}
                onChange={(integrationId, projectId, projectName) =>
                  setF({ ...f, name: f.name || projectName, config: { ...f.config, integrationId, projectId, projectName } })
                }
              />
            </div>
          )}
          {isPush && (
            <p className="text-sm text-fg-2 sm:col-span-2">
              After saving you'll get a unique URL. Call it from your cron job, backup script or worker each time it finishes; Hook alerts when the calls stop.
            </p>
          )}
          <Field label="Name" htmlFor="name">
            <Input id="name" value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="Marketing site" maxLength={100} required />
          </Field>
          <Field label="Group" htmlFor="group" hint="Optional, e.g. Vercel, Render, Databases">
            <Input id="group" value={f.group} onChange={(e) => set("group", e.target.value)} list="groups" maxLength={50} />
            <datalist id="groups">
              {meta.data?.groups.map((g) => <option key={g} value={g} />)}
            </datalist>
          </Field>
        </div>
      </Card>

      {isUrl && (
        <Card>
          <CardTitle>Request</CardTitle>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Method" htmlFor="method">
              <Select id="method" value={f.config.method} onChange={(e) => setC("method", e.target.value)}>
                {METHODS.map((m) => (
                  <option key={m}>{m}</option>
                ))}
              </Select>
            </Field>
            <Field label="Accepted status codes" htmlFor="codes" hint="Ranges or classes: 200-299, 301, 2xx">
              <Input id="codes" value={f.config.expectedStatus ?? ""} onChange={(e) => setC("expectedStatus", e.target.value)} placeholder="200-299" className="font-mono text-[13px]" />
            </Field>
            {f.type === "keyword" && (
              <>
                <Field label="Keyword" htmlFor="kw" hint="Case-sensitive, searched in the first 2 MB of the response.">
                  <Input id="kw" value={f.config.keyword ?? ""} onChange={(e) => setC("keyword", e.target.value)} required />
                </Field>
                <div className="flex items-end pb-2">
                  <Toggle checked={f.config.keywordInvert} onChange={(v) => setC("keywordInvert", v)} label="Fail if found" hint="Use for error text like “Maintenance”" />
                </div>
              </>
            )}
            {hasBody && (
              <div className="sm:col-span-2">
                <Field label="Body" htmlFor="body">
                  <Textarea id="body" value={f.config.body ?? ""} onChange={(e) => setC("body", e.target.value)} placeholder='{"ping": true}' />
                </Field>
              </div>
            )}
          </div>

          <div className="mt-5">
            <div className="mb-2 flex items-center justify-between">
              <span className="text-[13px] font-medium text-fg-2">Headers</span>
              <Button size="sm" variant="ghost" onClick={() => set("headers", [...f.headers, ["", ""]])}>
                + Add header
              </Button>
            </div>
            {f.headers.length === 0 && <p className="text-xs text-fg-3">None. Add Authorization or a custom Host here; values are encrypted at rest and never shown again.</p>}
            <div className="flex flex-col gap-2">
              {f.headers.map(([k, v], i) => (
                <div key={i} className="flex gap-2">
                  <Input aria-label="Header name" value={k} placeholder="Authorization" className="w-2/5 font-mono text-[13px]" onChange={(e) => set("headers", f.headers.map((h, j) => (j === i ? [e.target.value, h[1]] : h)))} />
                  <Input aria-label="Header value" value={v} placeholder="Bearer …" type={v === MASK ? "password" : "text"} className="flex-1 font-mono text-[13px]" onChange={(e) => set("headers", f.headers.map((h, j) => (j === i ? [h[0], e.target.value] : h)))} />
                  <Button size="sm" variant="ghost" className="h-10" aria-label="Remove header" onClick={() => set("headers", f.headers.filter((_, j) => j !== i))}>
                    ✕
                  </Button>
                </div>
              ))}
            </div>
          </div>

          <div className="mt-5 grid gap-4 sm:grid-cols-3">
            <Toggle checked={f.config.followRedirects} onChange={(v) => setC("followRedirects", v)} label="Follow redirects" />
            <Toggle checked={f.config.certAlerts} onChange={(v) => setC("certAlerts", v)} label="Certificate alerts" hint="14, 7, 3 and 1 day before expiry" />
            <Toggle checked={f.config.ignoreTls} onChange={(v) => setC("ignoreTls", v)} label="Ignore TLS errors" hint="Self-signed or expired certs" />
          </div>
        </Card>
      )}

      {f.type === "dns" && (
        <Card>
          <CardTitle>DNS</CardTitle>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Record type" htmlFor="rr">
              <Select id="rr" value={f.config.dnsRecord} onChange={(e) => setC("dnsRecord", e.target.value)}>
                {(meta.data?.dnsRecords ?? ["A", "AAAA", "CNAME", "MX", "TXT", "NS"]).map((r) => (
                  <option key={r}>{r}</option>
                ))}
              </Select>
            </Field>
            <Field label="Expected value" htmlFor="exp" hint="Optional. Fails if no record matches.">
              <Input id="exp" value={f.config.dnsExpected ?? ""} onChange={(e) => setC("dnsExpected", e.target.value)} placeholder="76.76.21.21" className="font-mono text-[13px]" />
            </Field>
            <Field label="Resolver" htmlFor="res" hint="Optional, e.g. 1.1.1.1. Blank uses the system's.">
              <Input id="res" value={f.config.dnsResolver ?? ""} onChange={(e) => setC("dnsResolver", e.target.value)} placeholder="1.1.1.1" className="font-mono text-[13px]" />
            </Field>
          </div>
        </Card>
      )}

      {f.type === "tls" && (
        <Card>
          <CardTitle>Certificate</CardTitle>
          <div className="grid gap-4 sm:grid-cols-2">
            <Toggle checked={f.config.certAlerts} onChange={(v) => setC("certAlerts", v)} label="Expiry alerts" hint="14, 7, 3 and 1 day before expiry" />
            <Toggle checked={f.config.ignoreTls} onChange={(v) => setC("ignoreTls", v)} label="Skip verification" hint="Only track expiry; don't fail on an invalid chain" />
          </div>
        </Card>
      )}

      <Card>
        <CardTitle>Timing</CardTitle>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Field label={isPush ? "Expect a push every" : "Check every"} htmlFor="interval" hint={isPush ? "Alert after this long (+10%) without a call" : undefined}>
            <Select
              id="interval"
              value={f.interval}
              onChange={(e) => {
                const v = Number(e.target.value);
                // a timeout can't outlast the interval; pull it down with it
                setF({ ...f, interval: v, timeout: Math.min(f.timeout, v) });
              }}
            >
              {intervalOptions.map((s) => (
                <option key={s} value={s}>
                  {fmtInterval(s)}
                </option>
              ))}
            </Select>
          </Field>
          {!isPush && (
            <Field label="Timeout (seconds)" htmlFor="timeout" hint={`Max ${Math.min(120, f.interval)}s`}>
              <Input id="timeout" type="number" min={1} max={Math.min(120, f.interval)} value={f.timeout} onChange={(e) => set("timeout", Number(e.target.value))} />
            </Field>
          )}
          <Field label={isPush ? "Missed pushes before down" : "Retries before down"} htmlFor="retries" hint="Failures to confirm before alerting">
            <Input id="retries" type="number" min={0} max={10} value={f.retries} onChange={(e) => set("retries", Number(e.target.value))} />
          </Field>
          {!isPush && (
            <Field label="Retry every" htmlFor="retryInterval" hint="While a failure is being confirmed">
              <Select id="retryInterval" value={f.retryInterval} onChange={(e) => set("retryInterval", Number(e.target.value))}>
                {(INTERVALS.includes(f.retryInterval) ? INTERVALS : [...INTERVALS, f.retryInterval].sort((a, b) => a - b)).map((s) => (
                  <option key={s} value={s}>
                    {fmtInterval(s)}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          {hasTarget(f.type) && (
            <div className="sm:col-span-2 lg:col-span-3">
              <Toggle
                checked={!f.config.noConfirm}
                onChange={(v) => setC("noConfirm", !v)}
                label="Confirm failures from other locations"
                hint={
                  f.headers.some(([k]) => k.trim())
                    ? "Not used for this monitor: custom headers (often auth) never leave the hub."
                    : meta.data?.locations?.length
                      ? `Before marking it down, ${meta.data.locations.join(", ")} re-check it. If they all reach it, it shows as Slow instead of paging you.`
                      : "No probe locations connected yet. Install the agent with --probes on a server to add one."
                }
              />
            </div>
          )}
          {!isPush && !isPlatform(f.type) && (
            <Field label="Slow above (ms)" htmlFor="slow" hint="Marks the check “Slow”. 0 turns it off.">
              <Input id="slow" type="number" min={0} max={120000} step={50} value={f.degradedMs} onChange={(e) => set("degradedMs", Number(e.target.value))} />
            </Field>
          )}
        </div>
      </Card>

      <Card>
        <CardTitle>Notify</CardTitle>
        {channels.data && channels.data.length > 0 ? (
          <div className="grid gap-2 sm:grid-cols-2">
            {channels.data.map((c) => {
              const on = f.channelIds.includes(c.id);
              return (
                <label key={c.id} className={`flex cursor-pointer items-center gap-3 rounded-2xl border p-3 transition ${on ? "border-series bg-series/10" : "border-line hover:bg-surface-2"}`}>
                  <input type="checkbox" className="size-4 accent-[var(--series)]" checked={on} onChange={() => set("channelIds", on ? f.channelIds.filter((x) => x !== c.id) : [...f.channelIds, c.id])} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{c.name}</span>
                    <span className="text-xs capitalize text-fg-3">
                      {c.type}
                      {!c.active && " · off"}
                    </span>
                  </span>
                </label>
              );
            })}
          </div>
        ) : (
          <p className="text-sm text-fg-2">
            No alert channels yet.{" "}
            <Link href="/alerts" className="text-series hover:underline">
              Add Telegram, Discord, email and more
            </Link>{" "}
            — this monitor will still be tracked.
          </p>
        )}
      </Card>

      <ErrorNote>{error}</ErrorNote>
      <div className="flex justify-end gap-2">
        <Link href={editing ? `/monitors/${id}` : "/monitors"}>
          <Button>Cancel</Button>
        </Link>
        <Button type="submit" variant="primary" loading={save.isPending}>
          {editing ? "Save changes" : "Create monitor"}
        </Button>
      </div>
    </form>
  );
}
