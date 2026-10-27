import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { Link, useLocation } from "wouter";
import { Modal, useToast } from "../components/overlay";
import { StatusIcon } from "../components/status";
import { Button, ErrorNote, Field, Input, Spinner, Toggle } from "../components/ui";
import { api, type Channel, type Server, type Thresholds } from "../lib/api";

/** Install command block with a copy button. */
export function InstallCommand({ command }: { command: string }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast("Couldn't copy. Select the text and copy it manually.", "error");
    }
  };
  return (
    <div className="relative rounded-2xl border border-line-strong bg-bg">
      <pre className="overflow-x-auto whitespace-pre-wrap break-all p-4 pr-24 font-mono text-[12.5px] leading-relaxed text-fg select-all">{command}</pre>
      <Button size="sm" className="absolute right-2.5 top-2.5" onClick={copy}>
        {copied ? "Copied" : "Copy"}
      </Button>
    </div>
  );
}

function ChannelPicker({ value, onChange }: { value: number[]; onChange: (ids: number[]) => void }) {
  const channels = useQuery({ queryKey: ["channels"], queryFn: () => api.get<Channel[]>("/channels") });
  if (!channels.data?.length) {
    return (
      <p className="text-sm text-fg-2">
        No alert channels yet.{" "}
        <Link href="/alerts" className="text-series hover:underline">
          Add one
        </Link>{" "}
        to get notified when this server goes offline or runs hot.
      </p>
    );
  }
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {channels.data.map((c) => {
        const on = value.includes(c.id);
        return (
          <label key={c.id} className={`flex cursor-pointer items-center gap-3 rounded-2xl border p-3 transition ${on ? "border-series bg-series/10" : "border-line hover:bg-surface-2"}`}>
            <input type="checkbox" className="size-4 accent-[var(--series)]" checked={on} onChange={() => onChange(on ? value.filter((x) => x !== c.id) : [...value, c.id])} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium">{c.name}</span>
              <span className="text-xs capitalize text-fg-3">{c.type}</span>
            </span>
          </label>
        );
      })}
    </div>
  );
}

/** Two steps: name the server, then run the install command and watch it connect. */
export function AddServerModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [, navigate] = useLocation();
  const channels = useQuery({ queryKey: ["channels"], queryFn: () => api.get<Channel[]>("/channels"), enabled: open });
  const [name, setName] = useState("");
  const [group, setGroup] = useState("");
  const [channelIds, setChannelIds] = useState<number[] | null>(null);
  const [created, setCreated] = useState<{ id: number; install: string } | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open && channelIds === null && channels.data) setChannelIds(channels.data.filter((c) => c.isDefault && c.active).map((c) => c.id));
  }, [open, channels.data, channelIds]);

  // Poll until the agent's first hello lands (SSE also refreshes this).
  const status = useQuery({
    queryKey: ["servers"],
    queryFn: () => api.get<Server[]>("/servers"),
    enabled: !!created,
    refetchInterval: created ? 3000 : false,
  });
  const server = created ? status.data?.find((s) => s.id === created.id) : undefined;
  const online = server?.status === "online";

  const create = useMutation({
    mutationFn: () => api.post<{ id: number; install: string }>("/servers", { name, group, channelIds: channelIds ?? [] }),
    onSuccess: (r) => {
      setCreated(r);
      qc.invalidateQueries({ queryKey: ["servers"] });
    },
    onError: (e) => setError((e as Error).message),
  });

  const close = () => {
    onClose();
    setTimeout(() => {
      setName("");
      setGroup("");
      setChannelIds(null);
      setCreated(null);
      setError("");
    }, 200);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    create.mutate();
  };

  return (
    <Modal open={open} onClose={close} title={created ? "Install the agent" : "Add a server"} width="max-w-2xl">
      {!created ? (
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name" htmlFor="srv-name">
              <Input id="srv-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="hetzner-fsn-1" maxLength={60} required />
            </Field>
            <Field label="Group" htmlFor="srv-group" hint="Optional, e.g. Production">
              <Input id="srv-group" value={group} onChange={(e) => setGroup(e.target.value)} maxLength={50} />
            </Field>
          </div>
          <div>
            <div className="mb-2 text-[13px] font-medium text-fg-2">Alert through</div>
            <ChannelPicker value={channelIds ?? []} onChange={setChannelIds} />
            <p className="mt-2 text-xs text-fg-3">Default alerts: offline after 60s, CPU or memory ≥ 90% or disk ≥ 85% for 5 minutes. You can change them later.</p>
          </div>
          <ErrorNote>{error}</ErrorNote>
          <div className="flex justify-end gap-2">
            <Button onClick={close}>Cancel</Button>
            <Button type="submit" variant="primary" loading={create.isPending}>
              Continue
            </Button>
          </div>
        </form>
      ) : (
        <div className="flex flex-col gap-4">
          <p className="text-sm text-fg-2">
            Run this on <b className="text-fg">{name}</b> as root. It installs a small read-only agent (~8 MB, ~15 MB RAM) as a systemd service. The agent only connects out to this hub; it opens no ports and never runs commands.
          </p>
          <InstallCommand command={created.install} />
          <p className="text-xs text-fg-3">
            The token in this command is shown once. If you lose it, open the server and choose “New install command”. Linux on amd64 or arm64 with systemd.
          </p>
          <div className={`flex items-center gap-3 rounded-2xl p-4 ${online ? "bg-up/10" : "bg-surface-2"}`}>
            {online ? <StatusIcon status="up" className="size-5" /> : <Spinner className="size-5 text-fg-2" />}
            <div className="text-sm">
              {online ? (
                <>
                  <b>Connected.</b> {server?.info.hostname} · {server?.info.platform}
                </>
              ) : (
                "Waiting for the agent to connect…"
              )}
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button onClick={close}>{online ? "Close" : "I'll do it later"}</Button>
            {online && (
              <Button
                variant="primary"
                onClick={() => {
                  close();
                  navigate(`/servers/${created.id}`);
                }}
              >
                Open server
              </Button>
            )}
          </div>
        </div>
      )}
    </Modal>
  );
}

/** Rename, thresholds, channels, token rotation and delete. */
export function EditServerModal({ server, open, onClose }: { server: Server; open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [, navigate] = useLocation();
  const [name, setName] = useState(server.name);
  const [group, setGroup] = useState(server.group);
  const [th, setTh] = useState<Thresholds>(server.thresholds);
  const [units, setUnits] = useState((server.thresholds.units ?? []).join(" "));
  const [channelIds, setChannelIds] = useState(server.channelIds);
  const [error, setError] = useState("");
  const [install, setInstall] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<"token" | "delete" | null>(null);

  useEffect(() => {
    if (open) {
      setName(server.name);
      setGroup(server.group);
      setTh(server.thresholds);
      setUnits((server.thresholds.units ?? []).join(" "));
      setChannelIds(server.channelIds);
      setInstall(null);
      setConfirm(null);
      setError("");
    }
  }, [open, server]);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["servers"] });
    qc.invalidateQueries({ queryKey: ["server", server.id] });
  };
  const save = useMutation({
    mutationFn: () => api.put(`/servers/${server.id}`, { name, group, thresholds: { ...th, units: units.split(/[\s,]+/).filter(Boolean) }, channelIds }),
    onSuccess: () => {
      toast("Server saved");
      refresh();
      onClose();
    },
    onError: (e) => setError((e as Error).message),
  });
  const rotate = useMutation({
    mutationFn: () => api.post<{ install: string }>(`/servers/${server.id}/token`),
    onSuccess: (r) => {
      setInstall(r.install);
      setConfirm(null);
      refresh();
    },
    onError: (e) => setError((e as Error).message),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/servers/${server.id}`),
    onSuccess: () => {
      toast("Server deleted");
      qc.removeQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      navigate("/servers");
    },
    onError: (e) => setError((e as Error).message),
  });

  const num = (k: "cpu" | "mem" | "disk" | "minutes") => (e: React.ChangeEvent<HTMLInputElement>) => setTh({ ...th, [k]: Number(e.target.value) });

  return (
    <Modal open={open} onClose={onClose} title={`Edit ${server.name}`} width="max-w-2xl">
      <form
        className="flex flex-col gap-5"
        onSubmit={(e) => {
          e.preventDefault();
          setError("");
          save.mutate();
        }}
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name" htmlFor="es-name">
            <Input id="es-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} required />
          </Field>
          <Field label="Group" htmlFor="es-group">
            <Input id="es-group" value={group} onChange={(e) => setGroup(e.target.value)} maxLength={50} />
          </Field>
        </div>

        <div>
          <div className="mb-2 text-[13px] font-medium text-fg-2">Alert when, for at least</div>
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Field label="CPU ≥ %" htmlFor="es-cpu">
              <Input id="es-cpu" type="number" min={0} max={100} value={th.cpu} onChange={num("cpu")} />
            </Field>
            <Field label="Memory ≥ %" htmlFor="es-mem">
              <Input id="es-mem" type="number" min={0} max={100} value={th.mem} onChange={num("mem")} />
            </Field>
            <Field label="Disk ≥ %" htmlFor="es-disk">
              <Input id="es-disk" type="number" min={0} max={100} value={th.disk} onChange={num("disk")} />
            </Field>
            <Field label="Minutes" htmlFor="es-min">
              <Input id="es-min" type="number" min={1} max={120} value={th.minutes} onChange={num("minutes")} />
            </Field>
          </div>
          <p className="mt-2 text-xs text-fg-3">0 turns a check off. Alerts clear once the value stays 5 points below the limit for a minute. Disk uses the fullest mount.</p>
          <div className="mt-3">
            <Toggle checked={th.offline} onChange={(v) => setTh({ ...th, offline: v })} label="Alert when the server stops reporting" hint="After 60 seconds without data" />
          </div>
          <div className="mt-3">
            <Toggle checked={th.containers} onChange={(v) => setTh({ ...th, containers: v })} label="Alert on container problems" hint="A running container stops, restarts, crash-loops or turns unhealthy" />
          </div>
          <div className="mt-4">
            <Field label="Watch systemd services" htmlFor="es-units" hint="Space or comma separated, e.g. nginx postgresql docker. Alerts when one isn't active.">
              <Input id="es-units" value={units} onChange={(e) => setUnits(e.target.value)} placeholder="nginx postgresql" className="font-mono text-[13px]" />
            </Field>
          </div>
          <div className="mt-3">
            <Toggle checked={th.services} onChange={(v) => setTh({ ...th, services: v })} label="Alert when a watched service stops" />
          </div>
        </div>

        <div>
          <div className="mb-2 text-[13px] font-medium text-fg-2">Alert through</div>
          <ChannelPicker value={channelIds} onChange={setChannelIds} />
        </div>

        <div className="rounded-2xl border border-line p-4">
          <div className="text-sm font-semibold">Agent access</div>
          {install ? (
            <div className="mt-3 flex flex-col gap-2">
              <p className="text-sm text-fg-2">The old token no longer works. Run this on the server to reconnect:</p>
              <InstallCommand command={install} />
            </div>
          ) : confirm === "token" ? (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <span className="text-sm text-fg-2">The current agent disconnects until you reinstall it.</span>
              <Button size="sm" variant="danger" loading={rotate.isPending} onClick={() => rotate.mutate()}>
                Replace token
              </Button>
              <Button size="sm" onClick={() => setConfirm(null)}>
                Cancel
              </Button>
            </div>
          ) : confirm === "delete" ? (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <span className="text-sm text-fg-2">Deletes the server and all its history.</span>
              <Button size="sm" variant="danger" loading={del.isPending} onClick={() => del.mutate()}>
                Delete server
              </Button>
              <Button size="sm" onClick={() => setConfirm(null)}>
                Cancel
              </Button>
            </div>
          ) : (
            <div className="mt-3 flex flex-wrap gap-2">
              <Button size="sm" onClick={() => setConfirm("token")}>
                New install command
              </Button>
              <Button size="sm" variant="ghost" className="text-down hover:text-down" onClick={() => setConfirm("delete")}>
                Delete server…
              </Button>
            </div>
          )}
        </div>

        <ErrorNote>{error}</ErrorNote>
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" loading={save.isPending}>
            Save
          </Button>
        </div>
      </form>
    </Modal>
  );
}
