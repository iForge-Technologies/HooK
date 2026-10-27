import { useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Button, DotMark, ErrorNote, Field, Input } from "../components/ui";
import { api, type AuthStatus } from "../lib/api";

/** First-run setup, sign-in, and the 2FA code step. */
export function AuthScreen({ status }: { status: AuthStatus }) {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const mode = status.setupRequired ? "setup" : status.mfaPending ? "totp" : "login";

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    if (mode === "setup" && password !== confirm) {
      setError("Passwords don't match.");
      return;
    }
    setBusy(true);
    try {
      if (mode === "setup") await api.post("/auth/setup", { username, password });
      else if (mode === "login") await api.post("/auth/login", { username, password });
      else await api.post("/auth/totp", { code });
      await qc.invalidateQueries({ queryKey: ["auth"] });
    } catch (err) {
      setError((err as Error).message);
      if (mode === "totp") setCode("");
    } finally {
      setBusy(false);
    }
  };

  const back = async () => {
    await api.post("/auth/logout").catch(() => {});
    qc.invalidateQueries({ queryKey: ["auth"] });
  };

  return (
    <div className="grid min-h-dvh place-items-center px-4 py-10">
      <div className="enter w-full max-w-[400px]">
        <div className="mb-8 flex flex-col items-center text-center">
          <DotMark className="mb-5 size-12" />
          <h1 className="text-2xl font-semibold tracking-[-0.03em]">
            {mode === "setup" ? "Set up Hook" : mode === "totp" ? "Two-factor code" : "Sign in to Hook"}
          </h1>
          <p className="mt-2 text-sm text-fg-2">
            {mode === "setup"
              ? "Create the admin account. This screen only appears once."
              : mode === "totp"
                ? "Enter the 6-digit code from your authenticator app."
                : "Uptime and server health, in one place."}
          </p>
        </div>
        <form onSubmit={submit} className="flex flex-col gap-4 rounded-[28px] border border-line bg-surface p-6">
          {mode === "totp" ? (
            <Field label="Code" htmlFor="code">
              <Input
                id="code"
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
                inputMode="numeric"
                autoComplete="one-time-code"
                placeholder="123456"
                className="tnum h-12 text-center font-mono text-xl tracking-[0.3em]"
                autoFocus
                required
              />
            </Field>
          ) : (
            <>
              <Field label="Username" htmlFor="username" hint={mode === "setup" ? "3–32 letters, digits, dots, dashes or underscores." : undefined}>
                <Input id="username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" autoFocus required />
              </Field>
              <Field label="Password" htmlFor="password" hint={mode === "setup" ? "At least 10 characters." : undefined}>
                <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={mode === "setup" ? "new-password" : "current-password"} required minLength={mode === "setup" ? 10 : undefined} />
              </Field>
              {mode === "setup" && (
                <Field label="Confirm password" htmlFor="confirm">
                  <Input id="confirm" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
                </Field>
              )}
            </>
          )}
          <ErrorNote>{error}</ErrorNote>
          <Button type="submit" variant="primary" loading={busy} className="mt-1 w-full">
            {mode === "setup" ? "Create account" : mode === "totp" ? "Verify" : "Sign in"}
          </Button>
          {mode === "totp" && (
            <button type="button" onClick={back} className="text-sm text-fg-2 hover:text-fg">
              Use a different account
            </button>
          )}
        </form>
        <p className="mt-6 text-center text-xs text-fg-3">Hook v{status.version}</p>
      </div>
    </div>
  );
}
