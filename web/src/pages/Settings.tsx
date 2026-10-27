import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useToast } from "../components/overlay";
import { StatusIcon } from "../components/status";
import { Button, Card, CardTitle, ErrorNote, Field, Input } from "../components/ui";
import { api, type AuthStatus } from "../lib/api";
import { KioskCard, NotificationsCard, SystemCard } from "./SettingsHub";

type User = NonNullable<AuthStatus["user"]>;

export function SettingsPage({ user, version }: { user: User; version: string }) {
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      <div>
        <h1 className="text-[28px] font-bold tracking-[-0.03em]">Settings</h1>
        <p className="mt-0.5 text-sm text-fg-2">
          Signed in as <b className="text-fg">{user.username}</b>
        </p>
      </div>
      <TwoFactor user={user} />
      <PasswordCard />
      <NotificationsCard />
      <KioskCard />
      <SystemCard />
      <Card>
        <CardTitle>About</CardTitle>
        <dl className="grid gap-3 text-sm sm:grid-cols-2">
          <div>
            <dt className="text-xs text-fg-3">Version</dt>
            <dd className="font-mono">{version}</dd>
          </div>
          <div>
            <dt className="text-xs text-fg-3">Data</dt>
            <dd className="text-fg-2">Raw checks kept 30 days, hourly summaries 2 years (HOOK_RAW_RETENTION_DAYS).</dd>
          </div>
        </dl>
      </Card>
    </div>
  );
}

function PasswordCard() {
  const toast = useToast();
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const save = useMutation({
    mutationFn: () => api.post("/account/password", { current: cur, new: next }),
    onSuccess: () => {
      setCur("");
      setNext("");
      setConfirm("");
      setError("");
      toast("Password changed. Other sessions were signed out.");
    },
    onError: (e) => setError((e as Error).message),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (next !== confirm) return setError("New passwords don't match.");
    save.mutate();
  };
  return (
    <Card>
      <CardTitle>Password</CardTitle>
      <form onSubmit={submit} className="grid gap-4 sm:grid-cols-3">
        <Field label="Current password" htmlFor="pw-cur">
          <Input id="pw-cur" type="password" value={cur} onChange={(e) => setCur(e.target.value)} autoComplete="current-password" required />
        </Field>
        <Field label="New password" htmlFor="pw-new" hint="At least 10 characters">
          <Input id="pw-new" type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" minLength={10} required />
        </Field>
        <Field label="Confirm" htmlFor="pw-conf">
          <Input id="pw-conf" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
        </Field>
        <div className="flex flex-col gap-3 sm:col-span-3">
          <ErrorNote>{error}</ErrorNote>
          <Button type="submit" loading={save.isPending} className="self-end">
            Change password
          </Button>
        </div>
      </form>
    </Card>
  );
}

function TwoFactor({ user }: { user: User }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [setup, setSetup] = useState<{ secret: string; qr: string } | null>(null);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [disabling, setDisabling] = useState(false);
  const [error, setError] = useState("");

  const done = (msg: string) => {
    setSetup(null);
    setDisabling(false);
    setCode("");
    setPassword("");
    setError("");
    toast(msg);
    qc.invalidateQueries({ queryKey: ["auth"] });
  };
  const begin = useMutation({
    mutationFn: () => api.post<{ secret: string; qr: string }>("/account/totp/begin"),
    onSuccess: (r) => {
      setSetup(r);
      setError("");
    },
    onError: (e) => setError((e as Error).message),
  });
  const enable = useMutation({
    mutationFn: () => api.post("/account/totp/enable", { code }),
    onSuccess: () => done("Two-factor authentication is on"),
    onError: (e) => setError((e as Error).message),
  });
  const disable = useMutation({
    mutationFn: () => api.post("/account/totp/disable", { password, code }),
    onSuccess: () => done("Two-factor authentication is off"),
    onError: (e) => setError((e as Error).message),
  });

  const codeInput = (
    <Field label="6-digit code" htmlFor="totp-code">
      <Input id="totp-code" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))} inputMode="numeric" autoComplete="one-time-code" placeholder="123456" className="tnum font-mono tracking-[0.2em]" required />
    </Field>
  );

  return (
    <Card>
      <CardTitle
        aside={
          <span className="inline-flex items-center gap-1.5 text-xs font-medium">
            <StatusIcon status={user.totpEnabled ? "up" : "degraded"} className="size-3.5" />
            {user.totpEnabled ? "On" : "Off"}
          </span>
        }
      >
        Two-factor authentication
      </CardTitle>
      <p className="text-sm text-fg-2">
        {user.totpEnabled
          ? "Signing in needs your password and a code from your authenticator app."
          : "Hook can see your whole infrastructure. Protect it with a code from an authenticator app (1Password, Authy, Google Authenticator)."}
      </p>

      {!user.totpEnabled && !setup && (
        <Button variant="primary" className="mt-4" loading={begin.isPending} onClick={() => begin.mutate()}>
          Set up two-factor
        </Button>
      )}

      {setup && (
        <form
          className="mt-5 grid gap-5 sm:grid-cols-[200px_1fr]"
          onSubmit={(e) => {
            e.preventDefault();
            enable.mutate();
          }}
        >
          <img src={setup.qr} alt="QR code for your authenticator app" className="size-[200px] rounded-2xl bg-white p-2" />
          <div className="flex flex-col gap-4">
            <ol className="list-decimal space-y-1 pl-5 text-sm text-fg-2">
              <li>Scan the QR code with your authenticator app.</li>
              <li>Or enter this key manually:</li>
            </ol>
            <code className="break-all rounded-xl bg-surface-2 px-3 py-2 font-mono text-xs select-all">{setup.secret}</code>
            {codeInput}
            <ErrorNote>{error}</ErrorNote>
            <div className="flex gap-2">
              <Button type="submit" variant="primary" loading={enable.isPending}>
                Turn on
              </Button>
              <Button onClick={() => setSetup(null)}>Cancel</Button>
            </div>
          </div>
        </form>
      )}

      {user.totpEnabled && !disabling && (
        <Button className="mt-4" onClick={() => setDisabling(true)}>
          Turn off…
        </Button>
      )}
      {disabling && (
        <form
          className="mt-5 grid gap-4 sm:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            disable.mutate();
          }}
        >
          <Field label="Password" htmlFor="totp-pw">
            <Input id="totp-pw" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
          </Field>
          {codeInput}
          <div className="flex flex-col gap-3 sm:col-span-2">
            <ErrorNote>{error}</ErrorNote>
            <div className="flex gap-2">
              <Button type="submit" variant="danger" loading={disable.isPending}>
                Turn off two-factor
              </Button>
              <Button onClick={() => setDisabling(false)}>Cancel</Button>
            </div>
          </div>
        </form>
      )}
      {!setup && !disabling && error && (
        <div className="mt-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}
    </Card>
  );
}
