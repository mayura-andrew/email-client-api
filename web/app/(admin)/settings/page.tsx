"use client";

import { FormEvent, useEffect, useState } from "react";
import { useDelivery } from "@/components/DeliveryContext";
import { Icon } from "@/components/Icon";
import { useToast } from "@/components/Toast";
import { Button, Dropzone, Skeleton, Spinner } from "@/components/ui";
import { api, json, Settings } from "@/lib/api";

export default function SettingsPage() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const load = () => api<Settings>("/api/v1/settings").then(setSettings);
  useEffect(() => { load().catch(() => undefined); }, []);
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
        <DeliveryCard />
      </div>
      {settings ? <TemplateCard settings={settings} onSaved={load} /> : <section className="panel"><Skeleton height={320} radius={12} /></section>}
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
      toast("success", "SMTP settings saved", "Every replica picks up the change immediately.");
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

function DeliveryCard() {
  const { data } = useDelivery();
  const rows = data ? [
    { label: "Send rate", value: data.limits.ratePerSecond ? `${data.limits.ratePerSecond} per second` : "Unlimited" },
    { label: "Daily limit", value: data.limits.dailyLimit ? `${data.limits.dailyLimit.toLocaleString()} per day` : "Unlimited" },
    { label: "Attempts per email", value: String(data.limits.maxAttempts) },
    { label: "Workers online", value: `${data.workers.online} (${data.workers.perReplica} per replica)` },
  ] : [];
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Delivery limits</h2><p className="muted small">Shared by every replica. Change them with environment variables such as <code>SMTP_DAILY_LIMIT</code>.</p></div></div>
      <dl className="summary">
        {data ? rows.map(row => <div key={row.label}><dt>{row.label}</dt><dd>{row.value}</dd></div>)
          : [0, 1, 2, 3].map(i => <div key={i}><dt><Skeleton width={90} /></dt><dd><Skeleton width={120} /></dd></div>)}
      </dl>
    </section>
  );
}
