"use client";

import { useRouter } from "next/navigation";
import { FormEvent, useCallback, useEffect, useState } from "react";
import { Icon } from "@/components/Icon";
import { Button, Spinner } from "@/components/ui";
import { saveSetup, SetupError, setupStatus, SetupStatus, testConnections, waitUntilStarted } from "@/lib/setup";

/**
 * First run, and recovery when PostgreSQL or Redis cannot be reached. The app
 * has no database yet at this point, so these settings are kept on this
 * computer.
 */
export default function SetupPage() {
  const router = useRouter();
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [failure, setFailure] = useState("");

  const follow = useCallback(async () => {
    try {
      const next = await waitUntilStarted();
      if (next.state === "running") router.replace("/login");
      else setStatus(next);
    } catch (err) { setFailure((err as Error).message); }
  }, [router]);

  useEffect(() => {
    setupStatus().then(next => {
      if (next.state === "running") router.replace("/login");
      else if (next.state === "starting") { setStatus(next); follow(); }
      else setStatus(next);
    }).catch(err => setFailure((err as Error).message));
  }, [router, follow]);

  return (
    <main className="login">
      <section className="login-art" aria-hidden="true">
        <div className="art-card art-card-1"><Icon name="server" size={20} /><span>PostgreSQL · campaigns and recipients</span></div>
        <div className="art-card art-card-2"><Icon name="zap" size={18} /><span>Redis · delivery queue</span></div>
        <div className="art-copy">
          <div className="eyebrow light">AI4ALL · CERTIFICATE OPERATIONS</div>
          <h2>Connect your workspace.</h2>
          <p>Campaigns, recipients and settings live in PostgreSQL. Redis runs the delivery queue. Use the connection URLs from your provider, for example Aiven.</p>
        </div>
      </section>
      <section className="login-panel">
        <div className="login-form setup-form">
          <span className="brand-mark">AI</span>
          {failure ? <p className="form-error"><Icon name="alert" size={14} /> {failure}</p>
            : !status || status.state === "starting" ? <Connecting />
            : <SetupForm status={status} onSaved={() => { setStatus({ ...status, state: "starting" }); follow(); }} />}
        </div>
      </section>
    </main>
  );
}

function Connecting() {
  return <>
    <h1>Connecting…</h1>
    <p className="muted"><Spinner /> Connecting to PostgreSQL and Redis and preparing the database. The first start can take a moment.</p>
  </>;
}

function SetupForm({ status, onSaved }: { status: SetupStatus; onSaved: () => void }) {
  const firstRun = status.needsConnections;
  const [form, setForm] = useState({ databaseUrl: "", redisUrl: "", adminEmail: "", adminPassword: "", confirm: "", secret: "" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<"" | "test" | "save" | "retry">("");
  const [tested, setTested] = useState<{ databaseUrl: string; redisUrl: string } | null>(null);
  const [showKey, setShowKey] = useState(false);
  const set = (key: keyof typeof form) => (value: string) => { setForm(f => ({ ...f, [key]: value })); setErrors(e => ({ ...e, [key]: "" })); setTested(null); };

  async function test() {
    setBusy("test"); setErrors({});
    try { setTested(await testConnections({ databaseUrl: form.databaseUrl, redisUrl: form.redisUrl })); }
    catch (err) { setErrors({ form: (err as Error).message }); }
    finally { setBusy(""); }
  }

  async function retry() {
    setBusy("retry");
    try {
      await fetch("/api/v1/setup/retry", { method: "POST" });
      onSaved();
    } finally { setBusy(""); }
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (status.needsAdmin && form.adminPassword !== form.confirm) { setErrors({ confirm: "Passwords do not match" }); return; }
    setBusy("save"); setErrors({});
    try {
      await saveSetup({
        databaseUrl: form.databaseUrl, redisUrl: form.redisUrl, secret: form.secret,
        ...(status.needsAdmin && { adminEmail: form.adminEmail, adminPassword: form.adminPassword }),
      });
      onSaved();
    } catch (err) {
      setErrors(err instanceof SetupError ? err.fields : { form: (err as Error).message });
    } finally { setBusy(""); }
  }

  const keep = !firstRun ? "Leave blank to keep the current URL" : undefined;
  return (
    <form onSubmit={submit} noValidate>
      <h1>{status.state === "error" ? "Can't connect" : firstRun ? "Set up the app" : "Create the admin account"}</h1>
      <p className="muted">{status.state === "error"
        ? "The app could not reach its database or Redis. Check your network, or update the connection URLs."
        : firstRun ? "Enter your database and Redis connections, then create the account you will sign in with."
        : "Create the account you will sign in with."}</p>

      {status.state === "error" && (
        <div className="banner banner-error setup-banner"><Icon name="alert" size={18} /><div>{status.error}</div></div>
      )}

      {(firstRun || status.state === "error") && <>
        <Field label="PostgreSQL URL" error={errors.databaseUrl} hint={!firstRun ? status.databaseUrl : undefined}>
          <input value={form.databaseUrl} onChange={e => set("databaseUrl")(e.target.value)} spellCheck={false}
            placeholder={keep ?? "postgres://avnadmin:…@….aivencloud.com:12345/defaultdb?sslmode=require"} />
        </Field>
        <Field label="Redis URL" error={errors.redisUrl} hint={!firstRun ? status.redisUrl : undefined}>
          <input value={form.redisUrl} onChange={e => set("redisUrl")(e.target.value)} spellCheck={false}
            placeholder={keep ?? "rediss://default:…@….aivencloud.com:12346"} />
        </Field>
        <div className="setup-test">
          <Button type="button" variant="secondary" icon="zap" loading={busy === "test"} onClick={test}
            disabled={firstRun && (!form.databaseUrl || !form.redisUrl)}>Test connections</Button>
          {tested && <TestResult result={tested} />}
        </div>
      </>}

      {firstRun && (
        <div className="setup-advanced">
          <button type="button" className="link-button" onClick={() => setShowKey(v => !v)}>
            {showKey ? "Hide" : "Using a database another computer already set up?"}
          </button>
          {showKey && (
            <Field label="Encryption key from the other computer" error={errors.secret}
              note="Find it in Settings → Connections on that computer. Without it, the saved SMTP password must be entered again.">
              <input value={form.secret} onChange={e => set("secret")(e.target.value)} spellCheck={false} />
            </Field>
          )}
        </div>
      )}

      {status.needsAdmin && <>
        <Field label="Admin email" error={errors.adminEmail}>
          <input type="email" autoComplete="username" value={form.adminEmail} onChange={e => set("adminEmail")(e.target.value)} />
        </Field>
        <Field label="Password" error={errors.adminPassword} note="At least 10 characters.">
          <input type="password" autoComplete="new-password" value={form.adminPassword} onChange={e => set("adminPassword")(e.target.value)} />
        </Field>
        <Field label="Confirm password" error={errors.confirm}>
          <input type="password" autoComplete="new-password" value={form.confirm} onChange={e => set("confirm")(e.target.value)} />
        </Field>
      </>}

      {errors.form && <p className="form-error"><Icon name="alert" size={14} /> {errors.form}</p>}
      <div className="setup-actions">
        {status.state === "error" && <Button type="button" variant="ghost" icon="refresh" loading={busy === "retry"} onClick={retry}>Retry</Button>}
        <Button type="submit" loading={busy === "save"} className={status.state === "error" ? "" : "btn-block"}>
          {status.state === "error" ? "Save and connect" : "Connect and continue"}
        </Button>
      </div>
    </form>
  );
}

function Field({ label, error, hint, note, children }: { label: string; error?: string; hint?: string; note?: string; children: React.ReactNode }) {
  return (
    <label className={`field ${error ? "has-error" : ""}`}>
      <span className="field-label">{label}{hint && <span className="field-hint">Current: {hint}</span>}</span>
      {children}
      {error ? <span className="field-error">{error}</span> : note && <span className="field-note">{note}</span>}
    </label>
  );
}

function TestResult({ result }: { result: { databaseUrl: string; redisUrl: string } }) {
  return (
    <ul className="setup-checks">
      {([["PostgreSQL", result.databaseUrl], ["Redis", result.redisUrl]] as const).map(([name, value]) => (
        <li key={name} className={value === "ok" ? "is-ok" : "is-bad"}>
          <Icon name={value === "ok" ? "check" : "alert"} size={13} /> {value === "ok" ? `${name} connected` : value}
        </li>
      ))}
    </ul>
  );
}
