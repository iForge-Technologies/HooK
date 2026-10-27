import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { Link } from "wouter";
import { Modal, useToast } from "../components/overlay";
import { Button, Card, Chip, Empty, ErrorNote, Field, Input, Select, Spinner, Toggle } from "../components/ui";
import { api, MASK, type Channel } from "../lib/api";

type FieldSpec = { key: string; label: string; secret?: boolean; placeholder?: string; hint?: ReactNode; options?: string[]; optional?: boolean };
type ChannelSpec = { label: string; blurb: string; fields: FieldSpec[]; note?: ReactNode };

const CHANNELS: Record<string, ChannelSpec> = {
  telegram: {
    label: "Telegram",
    blurb: "Message from your own bot",
    fields: [
      { key: "botToken", label: "Bot token", secret: true, placeholder: "123456:ABC-DEF…", hint: "Create a bot with @BotFather and paste its token." },
      { key: "chatId", label: "Chat ID", placeholder: "123456789", hint: "Message your bot, then open api.telegram.org/bot<token>/getUpdates and copy chat.id. Group IDs start with -." },
    ],
  },
  discord: {
    label: "Discord",
    blurb: "Rich embed in a channel",
    fields: [{ key: "webhookUrl", label: "Webhook URL", secret: true, placeholder: "https://discord.com/api/webhooks/…", hint: "Channel settings → Integrations → Webhooks → New webhook." }],
  },
  slack: {
    label: "Slack",
    blurb: "Incoming webhook message",
    fields: [{ key: "webhookUrl", label: "Webhook URL", secret: true, placeholder: "https://hooks.slack.com/services/…" }],
  },
  email: {
    label: "Email",
    blurb: "Plain text over SMTP",
    fields: [
      { key: "host", label: "SMTP host", placeholder: "smtp.resend.com" },
      { key: "port", label: "Port", placeholder: "587" },
      { key: "security", label: "Security", options: ["starttls", "tls", "none"], hint: "STARTTLS for 587, TLS for 465." },
      { key: "username", label: "Username", optional: true },
      { key: "password", label: "Password", secret: true, optional: true },
      { key: "from", label: "From", placeholder: "Hook <alerts@example.com>" },
      { key: "to", label: "To", placeholder: "you@example.com, oncall@example.com", hint: "Comma-separated." },
    ],
  },
  ntfy: {
    label: "ntfy",
    blurb: "Free push to your phone",
    fields: [
      { key: "server", label: "Server", placeholder: "https://ntfy.sh", optional: true },
      { key: "topic", label: "Topic", placeholder: "hook-alerts-7f3a", hint: "On ntfy.sh topics are public: pick a long, unguessable name." },
      { key: "token", label: "Access token", secret: true, optional: true },
    ],
  },
  browser: {
    label: "Browser push",
    blurb: "Notifications on your devices",
    fields: [],
    note: (
      <>
        Sends to every phone and computer that turned notifications on in{" "}
        <Link href="/settings" className="text-series underline">
          Settings
        </Link>
        . No third-party service involved.
      </>
    ),
  },
  webhook: {
    label: "Webhook",
    blurb: "JSON POST to your own service",
    fields: [
      { key: "url", label: "URL", placeholder: "https://example.com/hooks/uptime" },
      {
        key: "secret",
        label: "Signing secret",
        secret: true,
        optional: true,
        hint: (
          <>
            When set, requests carry <code className="font-mono">X-Hook-Signature: sha256=…</code> (HMAC of the body). Use this to forward alerts to your WhatsApp bot.
          </>
        ),
      },
    ],
  },
};

type Draft = { id?: number; name: string; type: string; config: Record<string, string>; isDefault: boolean; active: boolean; applyToAll: boolean };

export function AlertsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["channels"], queryFn: () => api.get<Channel[]>("/channels") });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [deleting, setDeleting] = useState<Channel | null>(null);

  const del = useMutation({
    mutationFn: (id: number) => api.del(`/channels/${id}`),
    onSuccess: () => {
      toast("Channel deleted");
      setDeleting(null);
      qc.invalidateQueries({ queryKey: ["channels"] });
      qc.invalidateQueries({ queryKey: ["monitors"] });
    },
    onError: (e) => toast((e as Error).message, "error"),
  });
  const test = useMutation({
    mutationFn: (c: Channel) => api.post("/channels/test", c),
    onSuccess: () => toast("Test notification sent"),
    onError: (e) => toast((e as Error).message, "error"),
  });

  const open = (c?: Channel) =>
    setDraft(c ? { ...c, config: { ...c.config }, applyToAll: false } : { name: "", type: "telegram", config: {}, isDefault: true, active: true, applyToAll: true });

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-[28px] font-bold tracking-[-0.03em]">Alerts</h1>
          <p className="mt-0.5 text-sm text-fg-2">Where Hook tells you about outages, recoveries and expiring certificates.</p>
        </div>
        <Button variant="primary" onClick={() => open()}>
          + Add channel
        </Button>
      </div>

      {q.isPending ? (
        <div className="grid h-40 place-items-center">
          <Spinner className="size-5" />
        </div>
      ) : q.data?.length === 0 ? (
        <Card>
          <Empty title="No alert channels" action={<Button variant="primary" onClick={() => open()}>Add channel</Button>}>
            Connect Telegram, Discord, Slack, email, ntfy, browser push or a webhook. Monitors only alert through channels you attach to them.
          </Empty>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {q.data?.map((c) => (
            <Card key={c.id} className="enter flex flex-col">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="truncate text-[15px] font-semibold tracking-[-0.02em]">{c.name}</div>
                  <div className="mt-0.5 text-xs text-fg-2">{CHANNELS[c.type]?.label ?? c.type}</div>
                </div>
                <div className="flex shrink-0 gap-1.5">
                  {c.isDefault && <Chip>Default</Chip>}
                  {!c.active && <Chip className="text-warn">Off</Chip>}
                </div>
              </div>
              <p className="mt-3 truncate font-mono text-xs text-fg-3">{summary(c)}</p>
              <div className="mt-5 flex flex-wrap gap-2 pt-1">
                <Button size="sm" onClick={() => test.mutate(c)} loading={test.isPending && test.variables?.id === c.id}>
                  Send test
                </Button>
                <Button size="sm" onClick={() => open(c)}>
                  Edit
                </Button>
                <Button size="sm" variant="ghost" className="ml-auto text-down hover:text-down" onClick={() => setDeleting(c)}>
                  Delete
                </Button>
              </div>
            </Card>
          ))}
        </div>
      )}

      {draft && <ChannelEditor draft={draft} onClose={() => setDraft(null)} />}

      <Modal open={!!deleting} onClose={() => setDeleting(null)} title="Delete channel?" width="max-w-sm">
        <p className="text-sm text-fg-2">
          <b className="text-fg">{deleting?.name}</b> will be removed from every monitor that uses it.
        </p>
        <div className="mt-6 flex justify-end gap-2">
          <Button onClick={() => setDeleting(null)}>Cancel</Button>
          <Button variant="danger" loading={del.isPending} onClick={() => deleting && del.mutate(deleting.id)}>
            Delete
          </Button>
        </div>
      </Modal>
    </div>
  );
}

function summary(c: Channel) {
  const k = c.config;
  switch (c.type) {
    case "telegram":
      return `chat ${k.chatId}`;
    case "email":
      return `${k.to} via ${k.host}`;
    case "ntfy":
      return `${k.server || "https://ntfy.sh"}/${k.topic}`;
    case "webhook":
      return k.url;
    case "browser":
      return "every device with notifications on";
    default:
      return "webhook configured";
  }
}

function ChannelEditor({ draft, onClose }: { draft: Draft; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [d, setD] = useState<Draft>(draft);
  const [error, setError] = useState("");
  const spec = CHANNELS[d.type];

  const save = useMutation({
    mutationFn: () => (d.id ? api.put(`/channels/${d.id}`, d) : api.post("/channels", d)),
    onSuccess: () => {
      toast(d.id ? "Channel saved" : "Channel added");
      qc.invalidateQueries({ queryKey: ["channels"] });
      qc.invalidateQueries({ queryKey: ["monitors"] });
      qc.invalidateQueries({ queryKey: ["monitor"] });
      onClose();
    },
    onError: (e) => setError((e as Error).message),
  });
  const test = useMutation({
    mutationFn: () => api.post("/channels/test", d),
    onSuccess: () => {
      setError("");
      toast("Test notification sent");
    },
    onError: (e) => setError((e as Error).message),
  });

  const setCfg = (k: string, v: string) => setD({ ...d, config: { ...d.config, [k]: v } });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    save.mutate();
  };

  return (
    <Modal open onClose={onClose} title={d.id ? `Edit ${draft.name}` : "Add alert channel"}>
      <form onSubmit={submit} className="flex flex-col gap-4">
        {!d.id && (
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Channel type">
            {Object.entries(CHANNELS).map(([k, c]) => (
              <button
                key={k}
                type="button"
                role="radio"
                aria-checked={d.type === k}
                aria-label={`${c.label}: ${c.blurb}`}
                onClick={() => setD({ ...d, type: k, config: k === "email" ? { port: "587", security: "starttls" } : {} })}
                className={`rounded-2xl border p-3 text-left transition ${d.type === k ? "border-series bg-series/10" : "border-line hover:bg-surface-2"}`}
              >
                <div className="text-sm font-semibold">{c.label}</div>
                <div className="mt-0.5 text-[11.5px] leading-snug text-fg-2">{c.blurb}</div>
              </button>
            ))}
          </div>
        )}
        <Field label="Name" htmlFor="ch-name">
          <Input id="ch-name" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} placeholder={`My ${spec.label}`} maxLength={60} required />
        </Field>
        {spec.note && <p className="rounded-2xl bg-surface-2 px-4 py-3 text-sm text-fg-2">{spec.note}</p>}
        <div className="grid gap-4 sm:grid-cols-2">
          {spec.fields.map((fs) => (
            <div key={fs.key} className={fs.hint || ["botToken", "webhookUrl", "url", "to"].includes(fs.key) ? "sm:col-span-2" : ""}>
              <Field label={fs.label + (fs.optional ? " (optional)" : "")} htmlFor={"cf-" + fs.key} hint={fs.hint}>
                {fs.options ? (
                  <Select id={"cf-" + fs.key} value={d.config[fs.key] ?? fs.options[0]} onChange={(e) => setCfg(fs.key, e.target.value)}>
                    {fs.options.map((o) => (
                      <option key={o} value={o}>
                        {o.toUpperCase()}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <Input
                    id={"cf-" + fs.key}
                    type={fs.secret ? "password" : "text"}
                    value={d.config[fs.key] ?? ""}
                    onChange={(e) => setCfg(fs.key, e.target.value)}
                    onFocus={(e) => fs.secret && e.target.value === MASK && setCfg(fs.key, "")}
                    placeholder={fs.placeholder}
                    autoComplete="off"
                    className="font-mono text-[13px]"
                    required={!fs.optional}
                  />
                )}
              </Field>
            </div>
          ))}
        </div>
        <div className="flex flex-col gap-3 rounded-2xl bg-surface-2 p-4">
          <Toggle checked={d.isDefault} onChange={(v) => setD({ ...d, isDefault: v })} label="Default for new monitors" hint="Pre-selected when you create a monitor" />
          {!d.id && <Toggle checked={d.applyToAll} onChange={(v) => setD({ ...d, applyToAll: v })} label="Attach to all existing monitors" />}
          <Toggle checked={d.active} onChange={(v) => setD({ ...d, active: v })} label="Enabled" hint="Turn off to mute this channel everywhere" />
        </div>
        <ErrorNote>{error}</ErrorNote>
        <div className="flex flex-wrap justify-end gap-2">
          <Button onClick={() => test.mutate()} loading={test.isPending} className="mr-auto">
            Send test
          </Button>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" loading={save.isPending}>
            {d.id ? "Save" : "Add channel"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
