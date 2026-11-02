"use client";

import { FormEvent, useEffect, useState } from "react";
import { useDelivery } from "@/components/DeliveryContext";
import { Icon } from "@/components/Icon";
import { useToast } from "@/components/Toast";
import { Button, Dropzone, Skeleton, Spinner } from "@/components/ui";
import { api, json, Settings } from "@/lib/api";
import { Connections, saveSetup, SetupError, SetupRequest, waitUntilStarted } from "@/lib/setup";

export default function SettingsPage() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [connections, setConnections] = useState<Connections | null>(null);
  const load = () => api<Settings>("/api/v1/settings").then(setSettings);
  useEffect(() => {
    load().catch(() => undefined);
    api<Connections>("/api/v1/setup/connections").then(setConnections).catch(() => undefined);
  }, []);
  return (
    <div className="page">
      <header className="page-header">
        <div>
          <div className="eyebrow">CONFIGURATION</div>
          <h1>Settings</h1>
        </div>
      </header>
      <div className="settings-layout">
        {settings ? <SmtpCard settings={settings} onSaved={load} /> : <section className="panel"><Skeleton height={380} radius={12} /></section>}
        {connections ? <DeliveryCard connections={connections} /> : <section className="panel"><Skeleton height={240} radius={12} /></section>}
      </div>
      {settings ? <TemplateCard settings={settings} onSaved={load} /> : <section className="panel"><Skeleton height={320} radius={12} /></section>}
      {connections && <div className="settings-layout">
        <ConnectionsCard connections={connections} />
        <AdminCard connections={connections} />
      </div>}
    </div>
  );
}

function SmtpCard({ settings, onSaved }: { settings: Settings; onSaved: () => Promise<void> }) {
  const toast = useToast();
  const [form, setForm] = useState({
    host: settings.smtp.host || "smtp.gmail.com", port: String(settings.smtp.port || 587),
    username: settings.smtp.username, password: "", sender: settings.smtp.sender,
  });
  const [saving, setSaving] = useState(false);
  const [testTo, setTestTo] = useState("");
  const [test, setTest] = useState<{ state: "idle" | "sending" | "ok" | "error"; message?: string }>({ state: "idle" });
  const dirty = form.host !== settings.smtp.host || Number(form.port) !== settings.smtp.port || form.username !== settings.smtp.username || form.sender !== settings.smtp.sender || !!form.password;

  async function save(event: FormEvent) {
    event.preventDefault();
    setSaving(true);
    try {
      await api("/api/v1/settings/smtp", { method: "PUT", body: json({ ...form, port: Number(form.port) }) });
      setForm(f => ({ ...f, password: "" }));
      await onSaved();
      toast("success", "SMTP settings saved", "New emails use them right away.");
    } catch (err) { toast("error", "Could not save SMTP settings", (err as Error).message); }
    finally { setSaving(false); }
  }

  async function sendTest() {
    setTest({ state: "sending" });
    try {
      await api("/api/v1/settings/smtp/test", { method: "POST", body: json({ recipient: testTo }) });
      setTest({ state: "ok", message: `Test email sent to ${testTo}. Check the inbox.` });
    } catch (err) { setTest({ state: "error", message: (err as Error).message }); }
  }

  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Sending account</h2><p className="muted small">Use a Google App Password, not your account password. It is encrypted before it is stored.</p></div>
        <span className={`pill ${settings.smtp.configured ? "pill-green" : "pill-amber"}`}>{settings.smtp.configured ? "Configured" : "Not configured"}</span>
      </div>
      <form onSubmit={save} className="form-grid">
        <label className="field span-2">SMTP host<input value={form.host} onChange={e => setForm({ ...form, host: e.target.value })} required /></label>
        <label className="field">Port<input type="number" value={form.port} onChange={e => setForm({ ...form, port: e.target.value })} required /></label>
        <label className="field span-3">Account<input type="email" value={form.username} onChange={e => setForm({ ...form, username: e.target.value })} required /></label>
        <label className="field span-3">App password
          <input type="password" autoComplete="new-password" value={form.password} onChange={e => setForm({ ...form, password: e.target.value })}
            placeholder={settings.smtp.configured ? "Leave blank to keep the current password" : "16-character app password"} />
        </label>
        <label className="field span-3">Send as<input type="email" value={form.sender} onChange={e => setForm({ ...form, sender: e.target.value })} required /></label>
        <div className="span-3 form-actions"><Button type="submit" loading={saving} disabled={!dirty}>Save changes</Button></div>
      </form>
      <div className="test-send">
        <h3>Send a test email</h3>
        <div className="inline-form">
          <input type="email" placeholder="you@example.com" value={testTo} onChange={e => { setTestTo(e.target.value); setTest({ state: "idle" }); }} />
          <Button variant="secondary" icon="send" loading={test.state === "sending"} disabled={!testTo.includes("@") || !settings.smtp.configured} onClick={sendTest}>Send test</Button>
        </div>
        {test.message && <p className={`test-result ${test.state === "ok" ? "is-ok" : "is-bad"}`}><Icon name={test.state === "ok" ? "check" : "alert"} size={14} /> {test.message}</p>}
      </div>
    </section>
  );
}

/** Renders a certificate PDF for `name` from the saved template or a candidate file. */
function useCertificatePreview(name: string, candidate: File | null, version: number) {
  const [url, setUrl] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => {
    let objectUrl = "";
    let cancelled = false;
    setLoading(true); setError("");
    const timer = window.setTimeout(() => {
      const student = name.trim() || "Student Name";
      let request: Promise<Blob>;
      if (candidate) {
        const body = new FormData();
        body.append("template", candidate);
        body.append("student_name", student);
        request = api<Blob>("/api/v1/certificate/template/preview", { method: "POST", body });
      } else {
        request = api<Blob>(`/api/v1/certificate/preview?student_name=${encodeURIComponent(student)}`);
      }
      request
        .then(blob => { if (!cancelled) { objectUrl = URL.createObjectURL(blob); setUrl(objectUrl); } })
        .catch(err => { if (!cancelled) { setUrl(""); setError((err as Error).message); } })
        .finally(() => !cancelled && setLoading(false));
    }, 350);
    return () => { cancelled = true; window.clearTimeout(timer); if (objectUrl) URL.revokeObjectURL(objectUrl); };
  }, [name, candidate, version]);
  return { url, loading, error };
}

function CertificateFrame({ url, loading, error, label }: { url: string; loading: boolean; error?: string; label?: string }) {
  return (
    <div className="template-preview">
      {label && <span className="frame-label">{label}</span>}
      {url && !error && <iframe src={`${url}#toolbar=0&navpanes=0&view=Fit`} title={label ?? "Certificate preview"} />}
      {error && <div className="frame-error"><Icon name="alert" size={18} /><span>{error}</span></div>}
      {loading && !error && <div className="template-loading"><Spinner /> Rendering…</div>}
    </div>
  );
}

function TemplateCard({ settings, onSaved }: { settings: Settings; onSaved: () => Promise<void> }) {
  const toast = useToast();
  const [name, setName] = useState("Ada Lovelace");
  const [version, setVersion] = useState(0);
  const [candidate, setCandidate] = useState<File | null>(null);
  const [showOriginal, setShowOriginal] = useState(false);
  const [originalUrl, setOriginalUrl] = useState("");
  const [saving, setSaving] = useState(false);
  const current = useCertificatePreview(name, null, version);
  const proposed = useCertificatePreview(name, candidate, version);

  useEffect(() => {
    if (!candidate) { setOriginalUrl(""); return; }
    const objectUrl = URL.createObjectURL(candidate);
    setOriginalUrl(objectUrl);
    return () => URL.revokeObjectURL(objectUrl);
  }, [candidate]);

  function choose(file: File) {
    const extension = file.name.toLowerCase().split(".").pop();
    if (!["pdf", "jpg", "jpeg"].includes(extension ?? "")) { toast("error", "Unsupported file", "Choose a PDF or JPG template."); return; }
    if (file.size > 20 * 1024 * 1024) { toast("error", "File too large", "Templates must be 20 MB or smaller."); return; }
    setShowOriginal(false);
    setCandidate(file);
  }

  async function save() {
    if (!candidate) return;
    setSaving(true);
    const body = new FormData();
    body.append("template", candidate);
    try {
      await api("/api/v1/certificate/template", { method: "POST", body });
      await onSaved();
      setCandidate(null);
      setVersion(v => v + 1);
      toast("success", "Template updated", "New certificates use it right away.");
    } catch (err) { toast("error", "Template rejected", (err as Error).message); }
    finally { setSaving(false); }
  }

  const isPDF = candidate?.name.toLowerCase().endsWith(".pdf");
  return (
    <section className={`panel ${candidate ? "is-reviewing" : ""}`}>
      <div className="panel-head">
        <div>
          <h2>Certificate template</h2>
          <p className="muted small">{candidate ? "Review the new template before it replaces the current one." : <>Current: <code>{settings.certificate.template || "none"}</code></>}</p>
        </div>
        {candidate && <span className="pill pill-amber">Not saved yet</span>}
      </div>

      {candidate ? <>
        <div className="candidate-file">
          <Icon name="file" size={18} />
          <div><strong>{candidate.name}</strong><span>{(candidate.size / 1024 / 1024).toFixed(2)} MB · {isPDF ? "PDF" : "JPG"}</span></div>
          <div className="segmented-control">
            <button className={!showOriginal ? "is-active" : ""} onClick={() => setShowOriginal(false)}>With name</button>
            <button className={showOriginal ? "is-active" : ""} onClick={() => setShowOriginal(true)}>Original file</button>
          </div>
        </div>
        <div className="compare">
          <CertificateFrame label="Current" {...current} />
          {showOriginal
            ? <div className="template-preview"><span className="frame-label is-new">New · original</span>
                {originalUrl && (isPDF ? <iframe src={`${originalUrl}#toolbar=0&navpanes=0&view=Fit`} title="Uploaded template" /> : <img src={originalUrl} alt="Uploaded template" />)}</div>
            : <CertificateFrame label="New" {...proposed} />}
        </div>
      </> : <CertificateFrame {...current} />}

      <label className="field">Preview with a name<input value={name} onChange={e => setName(e.target.value)} /></label>

      {candidate ? (
        <div className="candidate-actions">
          <Button variant="ghost" onClick={() => setCandidate(null)} disabled={saving}>Discard</Button>
          <Button icon="check" loading={saving} disabled={!!proposed.error || proposed.loading} onClick={save}>Use this template</Button>
        </div>
      ) : (
        <Dropzone accept=".pdf,.jpg,.jpeg,application/pdf,image/jpeg" onFile={choose} compact>
          <Icon name="upload" size={15} /> Choose a new PDF or JPG (max 20 MB) to preview it
        </Dropzone>
      )}
    </section>
  );
}

/**
 * Saves connection-level settings. The backend restarts to apply them, so this
 * waits for it and reloads; a new Redis means a new session, hence the login.
 */
function useSetupSave() {
  const toast = useToast();
  const [saving, setSaving] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  async function save(body: SetupRequest, done: string) {
    setSaving(true); setErrors({});
    try {
      await saveSetup(body);
      const status = await waitUntilStarted();
      if (status.state !== "running") { window.location.replace("/setup"); return; }
      toast("success", done);
      window.setTimeout(() => window.location.reload(), 600);
    } catch (err) {
      if (err instanceof SetupError) setErrors(err.fields);
      else toast("error", "Could not save", (err as Error).message);
      setSaving(false);
    }
  }
  return { save, saving, errors };
}

function DeliveryCard({ connections }: { connections: Connections }) {
  const { data } = useDelivery();
  const { save, saving, errors } = useSetupSave();
  const [form, setForm] = useState({ rate: String(connections.smtpRatePerSecond), daily: String(connections.smtpDailyLimit) });
  const dirty = Number(form.rate) !== connections.smtpRatePerSecond || Number(form.daily) !== connections.smtpDailyLimit;
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Delivery limits</h2><p className="muted small">Set these to your email provider&apos;s limits (Gmail: 500 a day, Brevo free: 300 a day). 0 means unlimited.</p></div></div>
      <form className="form-grid" onSubmit={event => { event.preventDefault(); save({ smtpRatePerSecond: Number(form.rate), smtpDailyLimit: Number(form.daily) }, "Delivery limits saved"); }}>
        <label className={`field ${errors.smtpRatePerSecond ? "has-error" : ""}`}>Emails per second
          <input type="number" min={0} step="0.1" value={form.rate} onChange={e => setForm({ ...form, rate: e.target.value })} />
          {errors.smtpRatePerSecond && <span className="field-error">{errors.smtpRatePerSecond}</span>}
        </label>
        <label className={`field ${errors.smtpDailyLimit ? "has-error" : ""}`}>Emails per day
          <input type="number" min={0} step="1" value={form.daily} onChange={e => setForm({ ...form, daily: e.target.value })} />
          {errors.smtpDailyLimit && <span className="field-error">{errors.smtpDailyLimit}</span>}
        </label>
        <div className="span-3 form-actions"><Button type="submit" loading={saving} disabled={!dirty}>Save limits</Button></div>
      </form>
      <dl className="summary">
        <div><dt>Attempts per email</dt><dd>{data ? data.limits.maxAttempts : <Skeleton width={40} />}</dd></div>
        <div><dt>Workers online</dt><dd>{data ? data.workers.online : <Skeleton width={40} />}</dd></div>
      </dl>
    </section>
  );
}

function ConnectionsCard({ connections }: { connections: Connections }) {
  const { save, saving, errors } = useSetupSave();
  const [form, setForm] = useState({ databaseUrl: "", redisUrl: "" });
  const [revealed, setRevealed] = useState(false);
  const [copied, setCopied] = useState(false);
  async function copy() {
    await navigator.clipboard.writeText(connections.secret).catch(() => undefined);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Connections</h2><p className="muted small">Stored on this computer in <code>{connections.configDir}</code>. Saving reconnects the app; sending pauses for a few seconds.</p></div></div>
      <form onSubmit={event => { event.preventDefault(); save(form, "Connections saved"); }}>
        <label className={`field ${errors.databaseUrl ? "has-error" : ""}`}><span className="field-label">PostgreSQL URL</span>
          <input value={form.databaseUrl} spellCheck={false} placeholder={connections.databaseUrl} onChange={e => setForm({ ...form, databaseUrl: e.target.value })} />
          {errors.databaseUrl ? <span className="field-error">{errors.databaseUrl}</span> : <span className="field-note">Leave blank to keep the current connection.</span>}
        </label>
        <label className={`field ${errors.redisUrl ? "has-error" : ""}`}><span className="field-label">Redis URL</span>
          <input value={form.redisUrl} spellCheck={false} placeholder={connections.redisUrl} onChange={e => setForm({ ...form, redisUrl: e.target.value })} />
          {errors.redisUrl ? <span className="field-error">{errors.redisUrl}</span> : <span className="field-note">Changing Redis signs you out.</span>}
        </label>
        <div className="form-actions"><Button type="submit" loading={saving} disabled={!form.databaseUrl && !form.redisUrl}>Save and reconnect</Button></div>
      </form>
      <div className="test-send">
        <h3>Encryption key</h3>
        <p className="muted small">Protects the saved SMTP password. Another computer using this database needs the same key, entered during its setup.</p>
        <div className="secret-row">
          <input readOnly value={revealed ? connections.secret : "•".repeat(24)} aria-label="Encryption key" />
          <Button type="button" variant="ghost" icon="eye" onClick={() => setRevealed(v => !v)}>{revealed ? "Hide" : "Show"}</Button>
          <Button type="button" variant="secondary" icon={copied ? "check" : "file"} onClick={copy}>{copied ? "Copied" : "Copy"}</Button>
        </div>
      </div>
    </section>
  );
}

function AdminCard({ connections }: { connections: Connections }) {
  const { save, saving, errors } = useSetupSave();
  const [form, setForm] = useState({ adminEmail: connections.adminEmail, adminPassword: "", confirm: "", currentPassword: "" });
  const [mismatch, setMismatch] = useState(false);
  const dirty = form.adminEmail !== connections.adminEmail || !!form.adminPassword;
  function submit(event: FormEvent) {
    event.preventDefault();
    if (form.adminPassword !== form.confirm) { setMismatch(true); return; }
    save({ adminEmail: form.adminEmail, adminPassword: form.adminPassword, currentPassword: form.currentPassword }, "Admin account updated");
  }
  const field = (key: keyof typeof form, label: string, type: string, autoComplete: string, error?: string, note?: string) => (
    <label className={`field ${error ? "has-error" : ""}`}><span className="field-label">{label}</span>
      <input type={type} autoComplete={autoComplete} value={form[key]} onChange={e => { setForm({ ...form, [key]: e.target.value }); setMismatch(false); }} />
      {error ? <span className="field-error">{error}</span> : note && <span className="field-note">{note}</span>}
    </label>
  );
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Admin account</h2><p className="muted small">The sign-in for this computer.</p></div></div>
      <form onSubmit={submit}>
        {field("adminEmail", "Email", "email", "username", errors.adminEmail)}
        {field("adminPassword", "New password", "password", "new-password", errors.adminPassword, "Leave blank to keep the current password.")}
        {form.adminPassword && field("confirm", "Confirm new password", "password", "new-password", mismatch ? "Passwords do not match" : undefined)}
        {dirty && field("currentPassword", "Current password", "password", "current-password", errors.currentPassword)}
        <div className="form-actions"><Button type="submit" loading={saving} disabled={!dirty || !form.currentPassword}>Update account</Button></div>
      </form>
    </section>
  );
}
