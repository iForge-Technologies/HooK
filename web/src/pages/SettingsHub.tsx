import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { Link } from "wouter";
import { Modal, useToast } from "../components/overlay";
import { StatusIcon } from "../components/status";
import { Button, Card, CardTitle, Chip, ErrorNote, Field, Input, Spinner } from "../components/ui";
import { api, type Channel } from "../lib/api";
import { fmtAgo, fmtBytes, fmtDateTime, fmtDuration, fmtInterval } from "../lib/format";
import { currentSubscription, disablePush, enablePush, pushSupport, thisDeviceId, type Device } from "../lib/push";

interface SystemInfo {
  version: string;
  health: { ok: boolean; checks: Record<string, { ok: boolean; detail: string }>; uptime: number; checked: number };
  deadman: { configured: boolean; urlHost?: string; every: number; lastPing: number; lastOk: boolean; lastError?: string; sentFail: boolean };
  dbBytes: number;
  dataDir: string;
  keyFromEnv: boolean;
  publicUrl: string;
}

interface KioskLink {
  id: number;
  name: string;
  createdAt: number;
  lastSeen: number;
}

const CHECK_LABEL: Record<string, string> = { database: "Database", scheduler: "Check scheduler", disk: "Data disk" };

export function NotificationsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const support = pushSupport();
  const [on, setOn] = useState<boolean | null>(null);
  const [error, setError] = useState("");
  const devices = useQuery({ queryKey: ["push-devices"], queryFn: () => api.get<Device[]>("/webpush/devices") });
  const channels = useQuery({ queryKey: ["channels"], queryFn: () => api.get<Channel[]>("/channels") });

  useEffect(() => {
    if (support !== "ok") return;
    currentSubscription()
      .then((s) => setOn(!!s && Notification.permission === "granted"))
      .catch(() => setOn(false));
  }, [support]);

  const refresh = () => qc.invalidateQueries({ queryKey: ["push-devices"] });
  const enable = useMutation({
    mutationFn: enablePush,
    onSuccess: () => {
      setOn(true);
      setError("");
      refresh();
      toast("Notifications are on for this device");
    },
    onError: (e) => setError((e as Error).message),
  });
  const disable = useMutation({
    mutationFn: disablePush,
    onSuccess: () => {
      setOn(false);
      setError("");
      refresh();
      toast("Notifications are off for this device");
    },
    onError: (e) => setError((e as Error).message),
  });
  const test = useMutation({
    mutationFn: () => api.post<{ sent: number }>("/webpush/test"),
    onSuccess: (r) => {
      setError("");
      toast(`Test sent to ${r.sent} device${r.sent === 1 ? "" : "s"}`);
      refresh();
    },
    onError: (e) => setError((e as Error).message),
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.del(`/webpush/devices/${id}`),
    onSuccess: () => {
      refresh();
      toast("Device removed");
    },
    onError: (e) => toast((e as Error).message, "error"),
  });

  const mine = thisDeviceId();
  const hasChannel = channels.data?.some((c) => c.type === "browser" && c.active);
  const list = devices.data ?? [];

  return (
    <Card>
      <CardTitle
        aside={
          support === "ok" &&
          on != null && (
            <span className="inline-flex items-center gap-1.5 text-xs font-medium">
              <StatusIcon status={on ? "up" : "paused"} className="size-3.5" />
              {on ? "On here" : "Off here"}
            </span>
          )
        }
      >
        Push notifications
      </CardTitle>
      <p className="text-sm text-fg-2">Get alerts as system notifications on this computer or phone, with no app or third-party service.</p>

      {support === "insecure" && <Note>Push needs HTTPS. Open Hook through its https:// address (or localhost) to turn this on.</Note>}
      {support === "unsupported" && <Note>This browser doesn't support Web Push.</Note>}
      {support === "ios-install" && (
        <Note>
          On iPhone and iPad, tap <b>Share → Add to Home Screen</b>, open Hook from the Home Screen, then turn notifications on there.
        </Note>
      )}

      {support === "ok" && (
        <div className="mt-4 flex flex-wrap gap-2">
          {on ? (
            <Button loading={disable.isPending} onClick={() => disable.mutate()}>
              Turn off on this device
            </Button>
          ) : (
            <Button variant="primary" loading={enable.isPending || on == null} onClick={() => enable.mutate()}>
              Turn on for this device
            </Button>
          )}
          {list.length > 0 && (
            <Button loading={test.isPending} onClick={() => test.mutate()}>
              Send test
            </Button>
          )}
        </div>
      )}
      {error && (
        <div className="mt-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}

      {list.length > 0 && (
        <ul className="mt-5 divide-y divide-line rounded-2xl border border-line">
          {list.map((d) => (
            <li key={d.id} className="flex items-center gap-3 px-4 py-3 text-sm">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate font-medium">{d.label || "Unnamed device"}</span>
                  {d.id === mine && on && <Chip>This device</Chip>}
                </div>
                <div className="mt-0.5 truncate text-xs text-fg-3">
                  {d.host} · added {fmtAgo(d.createdAt)}
                  {d.lastOk ? ` · last delivered ${fmtAgo(d.lastOk)}` : ""}
                  {d.failures > 0 && <span className="text-warn"> · {d.failures} failed</span>}
                </div>
              </div>
              <Button size="sm" variant="ghost" className="text-down hover:text-down" loading={remove.isPending && remove.variables === d.id} onClick={() => remove.mutate(d.id)}>
                Remove
              </Button>
            </li>
          ))}
        </ul>
      )}

      {list.length > 0 && channels.data && !hasChannel && (
        <Note>
          Devices only get alerts through a <b>Browser push</b> channel.{" "}
          <Link href="/alerts" className="text-series underline">
            Add one on the Alerts page
          </Link>{" "}
          and attach it to your monitors and servers.
        </Note>
      )}
    </Card>
  );
}

function Note({ children }: { children: React.ReactNode }) {
  return <p className="mt-4 rounded-2xl bg-surface-2 px-4 py-3 text-sm text-fg-2">{children}</p>;
}

export function SystemCard() {
  const q = useQuery({ queryKey: ["system"], queryFn: () => api.get<SystemInfo>("/system"), refetchInterval: 30_000 });
  if (!q.data) {
    return (
      <Card>
        <CardTitle>Hub health</CardTitle>
        {q.isError ? <ErrorNote>{(q.error as Error).message}</ErrorNote> : <Spinner />}
      </Card>
    );
  }
  const { health, deadman: dm } = q.data;
  return (
    <Card>
      <CardTitle
        aside={
          <span className="inline-flex items-center gap-1.5 text-xs font-medium">
            <StatusIcon status={health.ok ? "up" : "down"} className="size-3.5" />
            {health.ok ? "Healthy" : "Unhealthy"}
          </span>
        }
      >
        Hub health
      </CardTitle>
      <ul className="grid gap-2.5 text-sm">
        {Object.entries(health.checks).map(([k, c]) => (
          <li key={k} className="flex items-start gap-2.5">
            <StatusIcon status={c.ok ? "up" : "down"} className="mt-0.5 size-3.5 shrink-0" />
            <span className="w-32 shrink-0 font-medium">{CHECK_LABEL[k] ?? k}</span>
            <span className="min-w-0 text-fg-2">{c.detail}</span>
          </li>
        ))}
      </ul>
      <p className="mt-3 text-xs text-fg-3">
        Up {fmtDuration(health.uptime * 1000)} · the same checks answer <code className="font-mono">GET /healthz</code> (200 or 503) for load balancers.
      </p>

      <h3 className="mt-6 text-sm font-semibold">Dead-man switch</h3>
      {dm.configured ? (
        <p className="mt-1.5 flex items-start gap-2.5 text-sm text-fg-2">
          <StatusIcon status={!dm.lastPing ? "pending" : dm.lastOk && !dm.sentFail ? "up" : "down"} className="mt-0.5 size-3.5 shrink-0" />
          <span>
            Pinging <b className="font-mono text-fg">{dm.urlHost}</b> every {fmtInterval(dm.every)}.{" "}
            {dm.lastPing ? (
              <>
                Last ping {fmtAgo(dm.lastPing)}
                {dm.lastError ? <span className="text-down">: {dm.lastError}</span> : dm.sentFail ? ", reporting the hub as unhealthy." : ", OK."}
              </>
            ) : (
              "No ping sent yet."
            )}
          </span>
        </p>
      ) : (
        <p className="mt-1.5 text-sm text-fg-2">
          Off. Hook can't alert you if Hook itself is down. Set <code className="font-mono text-fg">HOOK_DEADMAN_URL</code> to a heartbeat URL from Healthchecks.io, Better Stack or Cronitor, and that service tells you when the pings stop.
        </p>
      )}

      <h3 className="mt-6 text-sm font-semibold">Data & backups</h3>
      <dl className="mt-1.5 grid gap-3 text-sm sm:grid-cols-2">
        <div>
          <dt className="text-xs text-fg-3">Database</dt>
          <dd>
            {fmtBytes(q.data.dbBytes)} in <span className="break-all font-mono text-xs">{q.data.dataDir}</span>
          </dd>
        </div>
        <div>
          <dt className="text-xs text-fg-3">Encryption key</dt>
          <dd className="text-fg-2">
            {q.data.keyFromEnv ? (
              <>
                From <code className="font-mono text-fg">HOOK_SECRET</code>. Keep a copy off this server.
              </>
            ) : (
              <>
                <code className="font-mono text-fg">secret.key</code> in the data folder. Back it up with the database.
              </>
            )}
          </dd>
        </div>
      </dl>
      <p className="mt-3 text-sm text-fg-2">
        Continuous: run the hub under Litestream (<code className="font-mono">deploy/litestream.yml</code>) to stream every change to S3 or R2. Nightly: <code className="font-mono text-fg">hook-hub backup /var/backups/hook</code> writes a consistent copy plus the key. Without the key, saved passwords and tokens can't be decrypted.
      </p>
    </Card>
  );
}

export function KioskCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["kiosk-links"], queryFn: () => api.get<KioskLink[]>("/kiosk-links") });
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [created, setCreated] = useState<{ url: string; name: string } | null>(null);
  const [revoking, setRevoking] = useState<KioskLink | null>(null);
  const [error, setError] = useState("");

  const create = useMutation({
    mutationFn: () => api.post<{ id: number; token: string }>("/kiosk-links", { name }),
    onSuccess: (r) => {
      setCreated({ url: `${location.origin}/kiosk/${r.token}`, name });
      setCreating(false);
      setName("");
      setError("");
      qc.invalidateQueries({ queryKey: ["kiosk-links"] });
    },
    onError: (e) => setError((e as Error).message),
  });
  const revoke = useMutation({
    mutationFn: (id: number) => api.del(`/kiosk-links/${id}`),
    onSuccess: () => {
      setRevoking(null);
      toast("Kiosk link revoked");
      qc.invalidateQueries({ queryKey: ["kiosk-links"] });
    },
    onError: (e) => toast((e as Error).message, "error"),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  const copy = (s: string) =>
    navigator.clipboard.writeText(s).then(
      () => toast("Link copied"),
      () => toast("Couldn't copy: select the link and copy it", "error"),
    );

  return (
    <Card>
      <CardTitle aside={!creating && <Button size="sm" onClick={() => setCreating(true)}>+ New link</Button>}>Kiosk mode</CardTitle>
      <p className="text-sm text-fg-2">
        A full-screen, read-only board for a wall TV or spare tablet. The link works without signing in and shows names and status only: never URLs, hosts or settings.
      </p>

      {creating && (
        <form onSubmit={submit} className="mt-4 flex flex-col gap-3 rounded-2xl bg-surface-2 p-4">
          <Field label="Screen name" htmlFor="kiosk-name" hint="So you know which link to revoke later.">
            <Input id="kiosk-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Office TV" maxLength={60} required autoFocus />
          </Field>
          <ErrorNote>{error}</ErrorNote>
          <div className="flex gap-2">
            <Button type="submit" variant="primary" loading={create.isPending}>
              Create link
            </Button>
            <Button onClick={() => setCreating(false)}>Cancel</Button>
          </div>
        </form>
      )}

      {q.data && q.data.length > 0 && (
        <ul className="mt-5 divide-y divide-line rounded-2xl border border-line">
          {q.data.map((k) => (
            <li key={k.id} className="flex items-center gap-3 px-4 py-3 text-sm">
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium">{k.name}</div>
                <div className="mt-0.5 text-xs text-fg-3">
                  Created {fmtDateTime(k.createdAt)} · {k.lastSeen ? `last viewed ${fmtAgo(k.lastSeen)}` : "never opened"}
                </div>
              </div>
              <Button size="sm" variant="ghost" className="text-down hover:text-down" onClick={() => setRevoking(k)}>
                Revoke
              </Button>
            </li>
          ))}
        </ul>
      )}

      <Modal open={!!created} onClose={() => setCreated(null)} title={`Kiosk link for ${created?.name ?? ""}`} width="max-w-lg">
        <p className="text-sm text-fg-2">Open this on the screen. It's shown only once; anyone with it can see the board until you revoke it.</p>
        <code className="mt-4 block break-all rounded-xl bg-surface-2 px-3 py-2.5 font-mono text-xs select-all">{created?.url}</code>
        <div className="mt-6 flex flex-wrap justify-end gap-2">
          <a href={created?.url} target="_blank" rel="noreferrer" className="mr-auto self-center text-sm text-series underline">
            Open board
          </a>
          <Button onClick={() => created && copy(created.url)}>Copy link</Button>
          <Button variant="primary" onClick={() => setCreated(null)}>
            Done
          </Button>
        </div>
      </Modal>

      <Modal open={!!revoking} onClose={() => setRevoking(null)} title="Revoke kiosk link?" width="max-w-sm">
        <p className="text-sm text-fg-2">
          <b className="text-fg">{revoking?.name}</b> stops updating within a few seconds.
        </p>
        <div className="mt-6 flex justify-end gap-2">
          <Button onClick={() => setRevoking(null)}>Cancel</Button>
          <Button variant="danger" loading={revoke.isPending} onClick={() => revoking && revoke.mutate(revoking.id)}>
            Revoke
          </Button>
        </div>
      </Modal>
    </Card>
  );
}
