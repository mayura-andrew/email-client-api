"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { FormEvent, Suspense, useEffect, useState } from "react";
import { Icon } from "@/components/Icon";
import { Button } from "@/components/ui";
import { api, json, sessionToken } from "@/lib/api";

export default function LoginPage() {
  return <Suspense><Login /></Suspense>;
}

function Login() {
  const router = useRouter();
  const next = useSearchParams().get("next") || "/";
  const [credentials, setCredentials] = useState({ email: "", password: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [shake, setShake] = useState(0);

  useEffect(() => {
    // Already signed in: skip the form.
    api("/api/v1/auth/session").then(() => router.replace(next)).catch(() => undefined);
  }, [router, next]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true); setError("");
    try {
      const { token } = await api<{ token: string }>("/api/v1/auth/login", { method: "POST", body: json(credentials) });
      sessionToken.set(token);
      router.replace(next.startsWith("/") ? next : "/");
    } catch (err) {
      const status = (err as { status?: number }).status;
      setError(status === 401 ? "That email and password don't match." : status === 429 ? "Too many attempts. Wait a minute and try again." : (err as Error).message);
      setShake(n => n + 1);
      setBusy(false);
    }
  }

  return (
    <main className="login">
      <section className="login-art" aria-hidden="true">
        <div className="art-card art-card-1"><Icon name="award" size={22} /><span>Certificate · Ada Lovelace</span></div>
        <div className="art-card art-card-2"><Icon name="check" size={18} /><span>Delivered to Grace Hopper</span></div>
        <div className="art-card art-card-3"><Icon name="send" size={18} /><span>298 of 300 sent</span></div>
        <div className="art-copy">
          <div className="eyebrow light">AI4ALL · CERTIFICATE OPERATIONS</div>
          <h2>Certificate delivery, without the spreadsheet anxiety.</h2>
          <p>Validate every recipient, preview every certificate, and watch each email leave the queue in real time.</p>
        </div>
      </section>
      <section className="login-panel">
        <form key={shake} onSubmit={submit} className={`login-form ${shake ? "shake" : ""}`}>
          <span className="brand-mark">AI</span>
          <h1>Welcome back</h1>
          <p className="muted">Sign in to the AI4ALL certificate workspace.</p>
          <label className="field">Email
            <input type="email" autoComplete="username" required autoFocus value={credentials.email}
              onChange={e => setCredentials({ ...credentials, email: e.target.value })} />
          </label>
          <label className="field">Password
            <input type="password" autoComplete="current-password" required value={credentials.password}
              onChange={e => setCredentials({ ...credentials, password: e.target.value })} />
          </label>
          {error && <p className="form-error"><Icon name="alert" size={14} /> {error}</p>}
          <Button type="submit" loading={busy} className="btn-block">Sign in</Button>
        </form>
      </section>
    </main>
  );
}
