"use client";

import { useRouter } from "next/navigation";
import { useEffect, useMemo, useRef, useState } from "react";
import { useDelivery } from "@/components/DeliveryContext";
import { Icon } from "@/components/Icon";
import { Button, Dropzone, Modal, Skeleton, Spinner } from "@/components/ui";
import { api, json } from "@/lib/api";
import { AudiencePreview } from "@/components/AudiencePreview";
import { analyzeAudience, Audience, detectMapping, Mapping, parseCSV, toUploadCSV } from "@/lib/csv";
import { etaSeconds } from "@/lib/eta";
import { formatDuration, formatNumber, initials, personalize } from "@/lib/format";

const STEPS = ["Audience", "Message", "Certificate", "Review & send"] as const;
const DEFAULT_BODY = "Hello {{student_name}},\n\nCongratulations on completing the program! Your certificate is attached to this email.\n\nWarm regards,\nThe AI4ALL Team";

type LaunchStep = { label: string; state: "waiting" | "active" | "done" | "error"; detail?: string };

export default function NewCampaignPage() {
  const router = useRouter();
  const { data: delivery } = useDelivery();
  const [step, setStep] = useState(0);
  const [file, setFile] = useState<File | null>(null);
  const [table, setTable] = useState<string[][] | null>(null);
  const [mapping, setMapping] = useState<Mapping>({ name: -1, email: -1 });
  // Email corrections the admin accepted, keyed by file line number.
  const [fixes, setFixes] = useState<Record<number, string>>({});
  const audience = useMemo(() => table ? analyzeAudience(table, mapping, fixes) : null, [table, mapping, fixes]);
  const [form, setForm] = useState({ name: "", subject: "Your certificate from AI4ALL", body: DEFAULT_BODY });
  const [sample, setSample] = useState(0);
  const [launch, setLaunch] = useState<LaunchStep[] | null>(null);
  const sent = useRef(false);

  const valid = audience?.valid ?? [];
  const recipient = valid[sample] ?? { name: "Student Name", email: "student@example.com", line: 0 };
  const canContinue = [valid.length > 0, !!form.name.trim() && !!form.subject.trim() && !!form.body.trim(), true, true][step];

  // Leaving with an imported audience would silently lose the work.
  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => { if (file && !sent.current) event.preventDefault(); };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [file]);

  async function onFile(next: File) {
    setFile(next);
    setSample(0);
    const parsed = parseCSV(await next.text());
    setTable(parsed);
    setMapping(detectMapping(parsed[0] ?? []));
    setFixes({});
    if (!form.name) setForm(f => ({ ...f, name: next.name.replace(/\.csv$/i, "").replace(/[-_]+/g, " ") }));
  }

  async function send() {
    if (!file) return;
    const steps: LaunchStep[] = [
      { label: "Creating campaign", state: "active" },
      { label: `Importing ${formatNumber(valid.length)} recipients`, state: "waiting" },
      { label: "Queuing certificates for delivery", state: "waiting" },
    ];
    const update = (index: number, patch: Partial<LaunchStep>) => {
      steps[index] = { ...steps[index], ...patch };
      setLaunch([...steps]);
    };
    setLaunch([...steps]);
    let campaignId = 0;
    let current = 0;
    try {
      const created = await api<{ campaign: { id: number } }>("/api/v1/campaigns", { method: "POST", body: json({ ...form, batchSize: 50 }) });
      campaignId = created.campaign.id;
      update(0, { state: "done" });

      current = 1; update(1, { state: "active" });
      const upload = new FormData();
      // Upload exactly what was previewed: mapped columns, accepted fixes, no
      // duplicates or invalid rows.
      upload.append("file", new Blob([toUploadCSV(valid)], { type: "text/csv" }), "audience.csv");
      const imported = await api<{ valid: number; invalidCount: number }>(`/api/v1/campaigns/${campaignId}/csv`, { method: "POST", body: upload });
      if (imported.valid === 0) throw new Error("The server accepted no recipients from this file.");
      update(1, { state: "done", detail: `${formatNumber(imported.valid)} accepted${imported.invalidCount ? `, ${imported.invalidCount} skipped` : ""}` });

      current = 2; update(2, { state: "active" });
      const started = await api<{ queued: number }>(`/api/v1/campaigns/${campaignId}/start`, { method: "POST" });
      update(2, { state: "done", detail: `${formatNumber(started.queued)} queued` });
      sent.current = true;
      window.setTimeout(() => router.push(`/campaigns/${campaignId}`), 900);
    } catch (err) {
      update(current, { state: "error", detail: (err as Error).message });
      if (campaignId) sent.current = true;
    }
  }

  const failedLaunch = launch?.some(s => s.state === "error");

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <div className="eyebrow">NEW CAMPAIGN</div>
          <h1>Send certificates</h1>
        </div>
      </header>

      <ol className="stepper">
        {STEPS.map((label, index) => (
          <li key={label} className={index < step ? "is-done" : index === step ? "is-current" : ""}>
            <button disabled={index > step && !(index === step + 1 && canContinue)} onClick={() => index <= step ? setStep(index) : canContinue && setStep(index)}>
              <span className="step-dot">{index < step ? <svg viewBox="0 0 24 24" width="14" height="14"><path className="draw-check" d="M5 12l5 5 9-10" /></svg> : index + 1}</span>
              <span>{label}</span>
            </button>
          </li>
        ))}
      </ol>

      <div className="wizard-body" key={step}>
        {step === 0 && <AudienceStep file={file} audience={audience} onFile={onFile}
          onMapping={next => { setMapping(next); setFixes({}); /* fixes belong to the previous email column */ }}
          onFix={next => setFixes(current => ({ ...current, ...next }))} />}
        {step === 1 && <MessageStep form={form} setForm={setForm} recipient={recipient} sample={sample} setSample={setSample} count={valid.length} />}
        {step === 2 && <CertificateStep recipient={recipient} sample={sample} setSample={setSample} count={valid.length} />}
        {step === 3 && <ReviewStep form={form} audience={audience!} recipient={recipient} delivery={delivery} />}
      </div>

      <footer className="wizard-footer">
        <Button variant="ghost" icon="back" onClick={() => setStep(s => s - 1)} disabled={step === 0}>Back</Button>
        <span className="muted small">{valid.length ? `${formatNumber(valid.length)} recipients · ` : ""}Step {step + 1} of {STEPS.length}</span>
        {step < STEPS.length - 1
          ? <Button onClick={() => setStep(s => s + 1)} disabled={!canContinue}>Continue <Icon name="arrow" size={15} /></Button>
          : <Button icon="send" onClick={send} disabled={!delivery?.smtpConfigured || !valid.length}>Send {formatNumber(valid.length)} certificates</Button>}
      </footer>

      <Modal open={!!launch} onClose={failedLaunch ? () => setLaunch(null) : undefined}>
        <div className="modal-body">
          <h2>{failedLaunch ? "Something went wrong" : launch?.every(s => s.state === "done") ? "Campaign is live" : "Launching campaign"}</h2>
          <ul className="launch-steps">
            {launch?.map(item => (
              <li key={item.label} className={`launch-${item.state}`}>
                <span className="launch-icon">
                  {item.state === "active" ? <Spinner size={16} /> : item.state === "done" ? <Icon name="check" size={15} /> : item.state === "error" ? <Icon name="x" size={15} /> : <i />}
                </span>
                <div><strong>{item.label}</strong>{item.detail && <p>{item.detail}</p>}</div>
              </li>
            ))}
          </ul>
          {launch?.every(s => s.state === "done") && <p className="muted small">Opening the live campaign view…</p>}
        </div>
        {failedLaunch && <div className="modal-actions"><Button variant="ghost" onClick={() => setLaunch(null)}>Close</Button></div>}
      </Modal>
    </div>
  );
}

function AudienceStep({ file, audience, onFile, onMapping, onFix }: {
  file: File | null; audience: Audience | null; onFile: (file: File) => void;
  onMapping: (mapping: Mapping) => void; onFix: (fixes: Record<number, string>) => void;
}) {
  if (!file || !audience) {
    return (
      <section className="panel">
        <Dropzone accept=".csv,text/csv" onFile={onFile}>
          <div className="dropzone-icon"><Icon name="upload" size={26} /></div>
          <strong>Drop your CSV here, or click to browse</strong>
          <span>Needs a name column and an email column, in any order. Every row is checked in your browser before anything is sent.</span>
        </Dropzone>
      </section>
    );
  }
  return (
    <div className="audience">
      <section className="panel file-card">
        <div className="file-chip"><Icon name="file" size={20} /><div><strong>{file.name}</strong>
          <span>{(file.size / 1024).toFixed(1)} KB · {formatNumber(audience.rows.length)} rows · {audience.headers.length} columns</span></div></div>
        <Dropzone accept=".csv,text/csv" onFile={onFile} compact><Icon name="refresh" size={14} /> Replace file</Dropzone>
      </section>
      <AudiencePreview audience={audience} onMapping={onMapping} onFix={onFix} />
    </div>
  );
}

function RecipientSwitcher({ sample, setSample, count, name }: { sample: number; setSample: (n: number) => void; count: number; name: string }) {
  return (
    <div className="switcher">
      <button onClick={() => setSample(Math.max(0, sample - 1))} disabled={sample === 0} aria-label="Previous recipient"><Icon name="back" size={14} /></button>
      <span>Previewing <strong>{name}</strong>{count > 1 && <span className="muted"> · {sample + 1} of {formatNumber(count)}</span>}</span>
      <button onClick={() => setSample(Math.min(count - 1, sample + 1))} disabled={sample >= count - 1} aria-label="Next recipient"><Icon name="arrow" size={14} /></button>
    </div>
  );
}

function MessageStep({ form, setForm, recipient, sample, setSample, count }: {
  form: { name: string; subject: string; body: string }; setForm: (f: { name: string; subject: string; body: string }) => void;
  recipient: { name: string; email: string }; sample: number; setSample: (n: number) => void; count: number;
}) {
  const body = useRef<HTMLTextAreaElement>(null);
  function insert(token: string) {
    const el = body.current;
    if (!el) return;
    const start = el.selectionStart, end = el.selectionEnd;
    setForm({ ...form, body: form.body.slice(0, start) + token + form.body.slice(end) });
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(start + token.length, start + token.length); });
  }
  const certificateName = `certificate-${recipient.name.replace(/[^a-z0-9_-]/gi, "-")}.pdf`;
  return (
    <div className="split message-step">
      <section className="panel">
        <label className="field"><span className="field-label">Campaign name <span className="field-hint">Only you see this</span></span>
          <input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} placeholder="October completion certificates" />
        </label>
        <label className="field">Subject
          <input value={form.subject} onChange={e => setForm({ ...form, subject: e.target.value })} />
        </label>
        <label className="field">Message
          <textarea ref={body} rows={11} value={form.body} onChange={e => setForm({ ...form, body: e.target.value })} />
        </label>
        <div className="tokens">
          <span className="muted small">Insert</span>
          <button type="button" onClick={() => insert("{{student_name}}")}>student name</button>
          <button type="button" onClick={() => insert("{{email}}")}>email</button>
        </div>
      </section>
      <section className="mail-preview">
        <RecipientSwitcher sample={sample} setSample={setSample} count={count} name={recipient.name} />
        <div className="mail">
          <div className="mail-head">
            <div className="mail-subject">{personalize(form.subject, recipient.name, recipient.email) || <span className="muted">No subject</span>}</div>
            <div className="mail-meta"><span className="avatar sm">AI</span><div><strong>AI4ALL</strong><span>to {recipient.email}</span></div></div>
          </div>
          <div className="mail-body">{personalize(form.body, recipient.name, recipient.email)}</div>
          <div className="mail-attachment"><Icon name="paperclip" size={15} /><span>{certificateName}</span><small>PDF</small></div>
        </div>
      </section>
    </div>
  );
}

function CertificateStep({ recipient, sample, setSample, count }: {
  recipient: { name: string }; sample: number; setSample: (n: number) => void; count: number;
}) {
  const [url, setUrl] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => {
    let objectUrl = "";
    let cancelled = false;
    setLoading(true); setError("");
    const timer = window.setTimeout(() => {
      api<Blob>(`/api/v1/certificate/preview?student_name=${encodeURIComponent(recipient.name)}`)
        .then(blob => { if (cancelled) return; objectUrl = URL.createObjectURL(blob); setUrl(objectUrl); })
        .catch(err => !cancelled && setError((err as Error).message))
        .finally(() => !cancelled && setLoading(false));
    }, 200);
    return () => { cancelled = true; window.clearTimeout(timer); if (objectUrl) URL.revokeObjectURL(objectUrl); };
  }, [recipient.name]);
  return (
    <section className="panel certificate-step">
      <div className="panel-head">
        <div><h2>Certificate preview</h2><p className="muted small">Rendered by the server with the same template and settings the workers use.</p></div>
        <RecipientSwitcher sample={sample} setSample={setSample} count={count} name={recipient.name} />
      </div>
      <div className="certificate-frame">
        {error ? <div className="banner banner-error"><Icon name="alert" size={18} /><div><strong>Preview failed.</strong> {error}</div></div>
          : <>
            {url && <iframe src={`${url}#toolbar=0&navpanes=0&view=Fit`} title="Certificate preview" />}
            {loading && <div className="certificate-loading"><Skeleton radius={10} /><span><Spinner /> Rendering certificate for {recipient.name}…</span></div>}
          </>}
      </div>
    </section>
  );
}

function ReviewStep({ form, audience, recipient, delivery }: {
  form: { name: string; subject: string }; audience: Audience; recipient: { name: string; email: string };
  delivery: ReturnType<typeof useDelivery>["data"];
}) {
  const count = audience.valid.length;
  const skipped = audience.counts.duplicate + audience.counts.invalid;
  const eta = etaSeconds(count, delivery);
  const days = delivery?.limits.dailyLimit ? Math.ceil(count / delivery.limits.dailyLimit) : 1;
  const checks = useMemo(() => [
    { ok: count > 0, label: `${formatNumber(count)} valid recipients`, detail: skipped ? `${skipped} rows skipped (${audience.counts.duplicate} duplicate, ${audience.counts.invalid} invalid)` : "No rows skipped" },
    ...(audience.counts.warning ? [{ ok: false, warn: true, label: `${audience.counts.warning} possible email typos`, detail: "They will be sent as written. Go back to Audience to apply the suggestions." }] : []),
    { ok: !!delivery?.smtpConfigured, label: delivery?.smtpConfigured ? "SMTP account configured" : "SMTP is not configured", detail: delivery?.smtpConfigured ? "Emails are sent from your configured sender" : "Add your sending account in Settings first" },
    { ok: days <= 1, warn: days > 1, label: days > 1 ? `Spans ${days} days of quota` : "Within today's sending quota", detail: delivery?.limits.dailyLimit ? `Daily limit ${formatNumber(delivery.limits.dailyLimit)}, ${formatNumber(delivery.limits.dailyUsed)} used today` : "No daily limit configured" },
  ], [count, skipped, audience.counts, delivery, days]);
  return (
    <div className="split review">
      <section className="panel">
        <h2>Summary</h2>
        <dl className="summary">
          <div><dt>Campaign</dt><dd>{form.name}</dd></div>
          <div><dt>Subject</dt><dd>{personalize(form.subject, recipient.name, recipient.email)}</dd></div>
          <div><dt>Recipients</dt><dd className="tabular">{formatNumber(count)}</dd></div>
          <div><dt>Attachment</dt><dd>Personalized PDF certificate</dd></div>
          <div><dt>Estimated time</dt><dd>{isFinite(eta) ? formatDuration(eta) : "Depends on SMTP speed"}{delivery?.limits.ratePerSecond ? <span className="muted small"> · limited to {delivery.limits.ratePerSecond}/s</span> : null}</dd></div>
        </dl>
      </section>
      <section className="panel">
        <h2>Pre-flight checks</h2>
        <ul className="checks">
          {checks.map(check => (
            <li key={check.label} className={check.ok ? "is-ok" : check.warn ? "is-warn" : "is-bad"}>
              <span><Icon name={check.ok ? "check" : "alert"} size={14} /></span>
              <div><strong>{check.label}</strong><p>{check.detail}</p></div>
            </li>
          ))}
        </ul>
        <p className="muted small reassure"><Icon name="lock" size={13} /> Delivery runs in the background with automatic retries. You can close this tab once it starts.</p>
      </section>
    </div>
  );
}
