"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { useDelivery } from "@/components/DeliveryContext";
import { Icon } from "@/components/Icon";
import { RecipientForm } from "@/components/RecipientForm";
import { useToast } from "@/components/Toast";
import { AnimatedNumber, Button, EmptyState, Modal, SegmentedProgress, Skeleton, StatusBadge } from "@/components/ui";
import { api, Campaign, PageMeta, Recipient } from "@/lib/api";
import { etaSeconds } from "@/lib/eta";
import { formatDate, formatDuration, initials } from "@/lib/format";
import { useNow, usePoll } from "@/lib/usePoll";

// Display order of recipient states, left to right in the progress bar.
const STATES = [
  { key: "sent", label: "Delivered" },
  { key: "sending", label: "Sending" },
  { key: "queued", label: "Queued" },
  { key: "retrying", label: "Retrying" },
  { key: "failed", label: "Failed" },
  { key: "valid", label: "Not queued" },
] as const;

const PAGE_SIZE = 100;

export default function CampaignPage() {
  const id = Number(useParams<{ id: string }>().id);
  const toast = useToast();
  const { data: delivery } = useDelivery();
  const now = useNow();
  const [live, setLive] = useState(true);
  const campaign = usePoll(() => api<{ campaign: Campaign }>(`/api/v1/campaigns/${id}`).then(r => r.campaign), live ? 2500 : 15000, [id]);
  const c = campaign.data;

  useEffect(() => { if (c) setLive(c.status === "running"); }, [c]);

  const [filter, setFilter] = useState("");
  const [search, setSearch] = useState("");
  const [debounced, setDebounced] = useState("");
  const [page, setPage] = useState(1);
  useEffect(() => { const t = window.setTimeout(() => { setDebounced(search); setPage(1); }, 250); return () => window.clearTimeout(t); }, [search]);
  const recipients = usePoll(
    () => api<{ recipients: Recipient[]; metadata: PageMeta }>(
      `/api/v1/campaigns/${id}/recipients?page=${page}&page_size=${PAGE_SIZE}&status=${filter}&q=${encodeURIComponent(debounced)}`),
    live ? 3000 : null, [id, filter, debounced, page]);

  const [busy, setBusy] = useState<"" | "start" | "retry">("");
  const [confirmStart, setConfirmStart] = useState(false);
  const [adding, setAdding] = useState(false);

  async function addRecipient(studentName: string, email: string) {
    const result = await api<{ queued: boolean }>(`/api/v1/campaigns/${id}/recipients`, { method: "POST", body: JSON.stringify({ studentName, email }) });
    toast("success", `${studentName} added`, result.queued ? "Their certificate is queued and will be sent shortly." : "They will be included when the campaign starts.");
    setAdding(false);
    if (result.queued) setLive(true);
    await Promise.all([campaign.refresh(), recipients.refresh()]);
  }

  async function start() {
    setBusy("start");
    try {
      const result = await api<{ queued: number }>(`/api/v1/campaigns/${id}/start`, { method: "POST" });
      toast("success", "Campaign started", `${result.queued} emails queued for delivery.`);
      setConfirmStart(false); setLive(true);
      await campaign.refresh();
    } catch (err) { toast("error", "Could not start the campaign", (err as Error).message); }
    finally { setBusy(""); }
  }

  async function retry() {
    setBusy("retry");
    try {
      const result = await api<{ requeued: number }>(`/api/v1/campaigns/${id}/retry`, { method: "POST" });
      await api(`/api/v1/campaigns/${id}/start`, { method: "POST" });
      toast("success", "Retrying failed recipients", `${result.requeued} emails are back in the queue.`);
      setLive(true); setFilter("");
      await campaign.refresh();
    } catch (err) { toast("error", "Retry failed", (err as Error).message); }
    finally { setBusy(""); }
  }

  if (campaign.error && !c) {
    return <div className="page"><EmptyState icon="alert" title="Campaign not found"
      action={<Link className="btn btn-ghost" href="/campaigns">Back to campaigns</Link>}>{campaign.error.message}</EmptyState></div>;
  }

  const breakdown = c?.breakdown ?? {};
  const total = Object.values(breakdown).reduce((sum, n) => sum + n, 0);
  const done = (breakdown.sent ?? 0) + (breakdown.failed ?? 0);
  const remaining = total - done - (breakdown.valid ?? 0);
  const percent = total ? (done / total) * 100 : 0;
  const elapsed = c?.startedAt ? ((c.completedAt ? new Date(c.completedAt).getTime() : now) - new Date(c.startedAt).getTime()) / 1000 : 0;

  return (
    <div className="page">
      <Link href="/campaigns" className="back-link"><Icon name="back" size={15} /> Campaigns</Link>
      <header className="page-header">
        <div>
          {c ? <>
            <div className="title-row"><h1>{c.name}</h1><StatusBadge status={c.status} /></div>
            <p className="muted">{c.subject}</p>
          </> : <><Skeleton width={320} height={40} /><Skeleton width={220} /></>}
        </div>
        <div className="header-actions">
          {c && <Button variant="secondary" icon="plus" onClick={() => setAdding(true)}>Add recipient</Button>}
          {c && c.failedCount > 0 && <Button variant="secondary" icon="refresh" loading={busy === "retry"} onClick={retry}>Retry {c.failedCount} failed</Button>}
          {c && (c.status === "draft" || c.status === "paused") && c.validCount > 0 &&
            <Button icon="send" onClick={() => setConfirmStart(true)}>{c.status === "draft" ? "Start sending" : "Resume"}</Button>}
        </div>
      </header>

      <section className="panel hero-progress">
        {c ? <>
          <div className="hero-top">
            <div>
              <div className="hero-percent"><AnimatedNumber value={percent} format={n => `${Math.floor(n)}`} /><span className="unit">%</span></div>
              <p className="muted">{done} of {total} recipients processed</p>
            </div>
            <dl className="hero-facts">
              <div><dt>{c.status === "completed" ? "Took" : "Elapsed"}</dt><dd>{elapsed ? formatDuration(elapsed) : "—"}</dd></div>
              <div><dt>Time left</dt><dd>{c.status === "running" ? (remaining > 0 ? `~${formatDuration(etaSeconds(remaining, delivery))}` : "Finishing") : "—"}</dd></div>
              <div><dt>Started</dt><dd>{formatDate(c.startedAt)}</dd></div>
              <div><dt>Completed</dt><dd>{formatDate(c.completedAt)}</dd></div>
            </dl>
          </div>
          <SegmentedProgress total={total} height={14} striped={c.status === "running"}
            segments={STATES.map(s => ({ key: s.key, value: breakdown[s.key] ?? 0 }))} />
          <div className="legend">
            {STATES.filter(s => breakdown[s.key]).map(s => (
              <button key={s.key} className={`legend-item ${filter === s.key ? "is-active" : ""}`}
                onClick={() => { setFilter(filter === s.key ? "" : s.key); setPage(1); }}>
                <i className={`dot dot-${s.key}`} />{s.label}<strong className="tabular"><AnimatedNumber value={breakdown[s.key] ?? 0} /></strong>
              </button>
            ))}
          </div>
        </> : <><Skeleton width={140} height={56} /><Skeleton height={14} radius={7} /></>}
      </section>

      <section className="panel flush">
        <div className="table-toolbar">
          <div className="segmented-control">
            <button className={filter === "" ? "is-active" : ""} onClick={() => { setFilter(""); setPage(1); }}>All<span className="count">{total}</span></button>
            {STATES.filter(s => breakdown[s.key]).map(s => (
              <button key={s.key} className={filter === s.key ? "is-active" : ""} onClick={() => { setFilter(s.key); setPage(1); }}>
                {s.label}<span className="count">{breakdown[s.key]}</span>
              </button>
            ))}
          </div>
          <label className="search">
            <Icon name="search" size={16} />
            <input placeholder="Search name or email" value={search} onChange={e => setSearch(e.target.value)} />
          </label>
        </div>
        {recipients.loading ? <div className="table-skeleton">{Array.from({ length: 6 }, (_, i) => <div key={i} className="skeleton-line"><Skeleton width={32} height={32} radius={16} /><Skeleton width="25%" /><Skeleton width="30%" /><Skeleton width={80} height={22} radius={11} /></div>)}</div>
          : recipients.data?.recipients.length === 0 ? <EmptyState icon="users" title="No recipients match">Change the filter or search.</EmptyState>
          : (
            <table className="table">
              <thead><tr><th>Student</th><th>Email</th><th>Status</th><th>Details</th></tr></thead>
              <tbody>
                {recipients.data?.recipients.map(r => (
                  <tr key={r.id} className={`row-${r.status}`}>
                    <td><div className="person"><span className="avatar">{initials(r.studentName)}</span>{r.studentName}</div></td>
                    <td className="muted">{r.email}</td>
                    <td><StatusBadge status={r.status} /></td>
                    <td className={r.status === "failed" ? "text-red small" : "muted small"} title={r.errorMessage}>{r.errorMessage ? truncate(r.errorMessage, 90) : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        {recipients.data && recipients.data.metadata.total > PAGE_SIZE && (
          <div className="pager inset">
            <button className="btn btn-ghost" disabled={page <= 1} onClick={() => setPage(p => p - 1)}>Previous</button>
            <span className="muted small">{(page - 1) * PAGE_SIZE + 1}–{Math.min(page * PAGE_SIZE, recipients.data.metadata.total)} of {recipients.data.metadata.total}</span>
            <button className="btn btn-ghost" disabled={page * PAGE_SIZE >= recipients.data.metadata.total} onClick={() => setPage(p => p + 1)}>Next</button>
          </div>
        )}
      </section>

      {c && (
        <details className="panel message-details">
          <summary><Icon name="mail" size={16} /> Message template</summary>
          <div className="message-preview">
            <div><span className="muted small">From</span> {c.sender}</div>
            <div><span className="muted small">Subject</span> {c.subject}</div>
            <pre>{c.body}</pre>
          </div>
        </details>
      )}

      <Modal open={adding} onClose={() => setAdding(false)} width={560}>
        <div className="modal-body">
          <div className="modal-icon"><Icon name="users" size={22} /></div>
          <h2>Add a recipient</h2>
          <p className="muted">
            {c?.status === "running" || c?.status === "completed"
              ? "For someone who was missed. Their certificate is generated and sent right away, with the same retries as everyone else."
              : `They will be included when you ${c?.status === "paused" ? "resume" : "start"} this campaign.`}
          </p>
          <RecipientForm existing={new Set()} autoFocus submitLabel="Add recipient" onAdd={addRecipient} />
        </div>
      </Modal>

      <Modal open={confirmStart} onClose={() => setConfirmStart(false)}>
        <div className="modal-body">
          <div className="modal-icon"><Icon name="send" size={22} /></div>
          <h2>Send {c?.validCount} certificates?</h2>
          <p className="muted">Each recipient gets a personalized PDF. Sending starts right away and can&apos;t be undone.</p>
          {c && delivery && <p className="estimate"><Icon name="clock" size={14} /> Estimated time: {formatDuration(etaSeconds(c.validCount, delivery))}</p>}
        </div>
        <div className="modal-actions">
          <Button variant="ghost" onClick={() => setConfirmStart(false)}>Cancel</Button>
          <Button icon="send" loading={busy === "start"} onClick={start}>Start sending</Button>
        </div>
      </Modal>
    </div>
  );
}

function truncate(value: string, max: number) {
  return value.length > max ? `${value.slice(0, max)}…` : value;
}
