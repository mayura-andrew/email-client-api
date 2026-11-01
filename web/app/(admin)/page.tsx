"use client";

import Link from "next/link";
import { ActivityFeed, LiveLabel, Pipeline, ThroughputChart } from "@/components/Delivery";
import { CampaignProgress, percentDone } from "@/components/CampaignProgress";
import { useDelivery } from "@/components/DeliveryContext";
import { Icon } from "@/components/Icon";
import { AnimatedNumber, EmptyState, Skeleton, StatusBadge } from "@/components/ui";
import { api, Campaign } from "@/lib/api";
import { etaSeconds } from "@/lib/eta";
import { formatDuration } from "@/lib/format";
import { useNow, usePoll } from "@/lib/usePoll";

export default function OverviewPage() {
  const { data: status, error, updatedAt } = useDelivery();
  const campaigns = usePoll(() => api<{ campaigns: Campaign[] }>("/api/v1/campaigns?page_size=20"), 5000);
  const now = useNow();
  const active = campaigns.data?.campaigns.filter(c => c.status === "running" || c.status === "paused") ?? [];
  const inQueue = status ? status.queue.ready + status.queue.scheduled + status.queue.published : 0;

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <div className="eyebrow">OVERVIEW</div>
          <h1>Delivery</h1>
        </div>
        <LiveLabel updatedAt={updatedAt} now={now} error={error} />
      </header>

      {status && !status.smtpConfigured && (
        <Link href="/settings" className="banner banner-warn">
          <Icon name="alert" size={18} />
          <div><strong>SMTP is not configured.</strong> Queued emails are held until you add your sending account.</div>
          <span className="banner-cta">Open settings <Icon name="arrow" size={14} /></span>
        </Link>
      )}

      <section className="kpis">
        <Kpi label="Delivered today" icon="check" tone="green" value={status?.today.sent}
          sub={status ? (status.limits.dailyLimit ? `${status.limits.dailyUsed} of ${status.limits.dailyLimit} daily quota` : "No daily quota set") : undefined} />
        <Kpi label="Waiting in queue" icon="clock" tone="slate" value={status ? inQueue : undefined}
          sub={status?.queue.scheduled ? `${status.queue.scheduled} scheduled for retry` : "Ready to send"} />
        <Kpi label="Sending now" icon="send" tone="blue" value={status?.queue.sending}
          sub={status ? `${status.workers.online} workers online` : undefined} />
        <Kpi label="Failed today" icon="alert" tone={status?.today.failed ? "red" : "slate"} value={status?.today.failed}
          sub={status ? `Permanent errors or ${status.limits.maxAttempts} attempts used` : undefined} />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <h2>Delivery pipeline</h2>
            <p className="muted small">Every email moves left to right. Retries return to the queue with backoff.</p>
          </div>
        </div>
        {status ? <Pipeline status={status} /> : <div className="pipeline">{[0, 1, 2, 3].map(i => <Skeleton key={i} height={128} radius={14} />)}</div>}
      </section>

      <div className="split">
        <section className="panel">
          <div className="panel-head"><h2>Throughput</h2></div>
          {status ? <ThroughputChart points={status.throughput} /> : <Skeleton height={190} radius={12} />}
        </section>
        <section className="panel">
          <div className="panel-head"><h2>Live activity</h2></div>
          {status ? <ActivityFeed events={status.recent} now={now} /> : <FeedSkeleton />}
        </section>
      </div>

      <section className="panel">
        <div className="panel-head">
          <h2>Active campaigns</h2>
          <Link href="/campaigns" className="text-link">All campaigns <Icon name="arrow" size={14} /></Link>
        </div>
        {campaigns.loading ? <FeedSkeleton rows={2} /> : active.length === 0 ? (
          <EmptyState icon="send" title="Nothing is sending right now"
            action={<Link className="btn btn-primary" href="/campaigns/new"><Icon name="plus" size={16} /><span>New campaign</span></Link>}>
            Start a campaign and you will see it move through the pipeline here.
          </EmptyState>
        ) : (
          <div className="active-list">
            {active.map(campaign => {
              const remaining = campaign.validCount - campaign.sentCount - campaign.failedCount;
              return (
                <Link key={campaign.id} href={`/campaigns/${campaign.id}`} className="active-row">
                  <div className="active-name">
                    <strong>{campaign.name}</strong>
                    <StatusBadge status={campaign.status} />
                  </div>
                  <CampaignProgress campaign={campaign} />
                  <div className="active-meta">
                    <span><strong className="tabular">{campaign.sentCount}</strong> / {campaign.validCount} delivered · {percentDone(campaign)}%</span>
                    <span>{remaining > 0 ? `~${formatDuration(etaSeconds(remaining, status))} left` : "Finishing up"}</span>
                  </div>
                </Link>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}

function Kpi({ label, value, sub, icon, tone }: { label: string; value?: number; sub?: string; icon: "check" | "clock" | "send" | "alert"; tone: string }) {
  return (
    <div className={`kpi kpi-${tone}`}>
      <div className="kpi-top"><span>{label}</span><span className="kpi-icon"><Icon name={icon} size={15} /></span></div>
      <div className="kpi-value">{value === undefined ? <Skeleton width={70} height={34} /> : <AnimatedNumber value={value} />}</div>
      <div className="kpi-sub">{sub ?? <Skeleton width={120} height={11} />}</div>
    </div>
  );
}

function FeedSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="stack">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="skeleton-row"><Skeleton width={28} height={28} radius={14} /><div className="stack-tight"><Skeleton width="60%" /><Skeleton width="35%" height={10} /></div></div>
      ))}
    </div>
  );
}
