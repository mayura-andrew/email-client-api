"use client";

import Link from "next/link";
import { useState } from "react";
import { DeliveryEvent, DeliveryStatus } from "@/lib/api";
import { timeAgo, timeUntil } from "@/lib/format";
import { Icon, IconName } from "./Icon";
import { AnimatedNumber, LiveDot } from "./ui";

type Stage = { key: string; label: string; value: number; hint: string; icon: IconName };

/**
 * The outbox as an operator sees it. Connectors animate only while work is
 * actually flowing between two stages, so motion always means something.
 */
export function Pipeline({ status }: { status: DeliveryStatus }) {
  const { queue, today, workers } = status;
  const stages: Stage[] = [
    { key: "queued", label: "Queued", value: queue.ready + queue.scheduled, icon: "clock",
      hint: queue.scheduled ? `${queue.scheduled} scheduled · next in ${timeUntil(status.nextRetry)}` : "Waiting in PostgreSQL" },
    { key: "dispatched", label: "Dispatched", value: queue.published, icon: "zap", hint: "In the Redis stream" },
    { key: "sending", label: "Sending", value: queue.sending, icon: "send",
      hint: `${workers.online} worker${workers.online === 1 ? "" : "s"} online` },
    { key: "delivered", label: "Delivered today", value: today.sent, icon: "check",
      hint: today.failed ? `${today.failed} failed today` : "No failures today" },
  ];
  const flowing = [queue.ready > 0, queue.published > 0, queue.sending > 0];
  return (
    <div className="pipeline">
      {stages.map((stage, index) => (
        <div key={stage.key} className="pipeline-step">
          <div className={`stage stage-${stage.key} ${stage.value > 0 && index < 3 ? "is-busy" : ""}`}>
            <div className="stage-icon"><Icon name={stage.icon} size={17} /></div>
            <div className="stage-value"><AnimatedNumber value={stage.value} /></div>
            <div className="stage-label">{stage.label}</div>
            <div className="stage-hint">{stage.hint}</div>
          </div>
          {index < stages.length - 1 && <div className={`connector ${flowing[index] ? "is-flowing" : ""}`}><span /></div>}
        </div>
      ))}
    </div>
  );
}

/** Per-minute delivered (stacked over failed) bars for the last 30 minutes. */
export function ThroughputChart({ points }: { points: DeliveryStatus["throughput"] }) {
  const [hover, setHover] = useState<number | null>(null);
  const max = Math.max(5, ...points.map(p => p.sent + p.failed));
  const lastFive = points.slice(-5);
  const perMinute = lastFive.reduce((sum, p) => sum + p.sent, 0) / Math.max(1, lastFive.length);
  const active = hover !== null ? points[hover] : null;
  return (
    <div className="chart">
      <div className="chart-head">
        <div>
          <div className="chart-value"><AnimatedNumber value={perMinute} format={n => n.toFixed(perMinute < 10 ? 1 : 0)} /><span className="unit"> / min</span></div>
          <div className="muted small">Average over the last 5 minutes</div>
        </div>
        <div className="chart-readout">
          {active ? <>
            <strong>{new Date(active.minute).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</strong>
            <span><i className="dot dot-sent" />{active.sent} delivered</span>
            <span><i className="dot dot-failed" />{active.failed} failed</span>
          </> : <span className="muted small">Hover a bar for details</span>}
        </div>
      </div>
      <div className="bars" onMouseLeave={() => setHover(null)}>
        {points.map((point, index) => {
          const total = point.sent + point.failed;
          return (
            <div key={point.minute} className={`bar ${hover === index ? "is-hover" : ""}`} onMouseEnter={() => setHover(index)}>
              <span className="bar-stack" style={{ height: `${(total / max) * 100}%` }}>
                <span className="bar-failed" style={{ flexGrow: point.failed }} />
                <span className="bar-sent" style={{ flexGrow: point.sent }} />
              </span>
            </div>
          );
        })}
      </div>
      <div className="chart-axis"><span>30 min ago</span><span>now</span></div>
    </div>
  );
}

const eventIcon: Record<DeliveryEvent["status"], IconName> = { sent: "check", failed: "x", sending: "send", retrying: "refresh" };
const eventVerb: Record<DeliveryEvent["status"], string> = { sent: "Delivered to", failed: "Failed for", sending: "Sending to", retrying: "Retrying" };

export function ActivityFeed({ events, now }: { events: DeliveryEvent[]; now: number }) {
  if (events.length === 0) return <p className="muted small feed-empty">Delivery events will stream in here once a campaign starts.</p>;
  return (
    <ul className="feed">
      {events.map(event => (
        <li key={`${event.id}-${event.status}-${event.attempts}`} className={`feed-item feed-${event.status}`}>
          <span className="feed-icon"><Icon name={eventIcon[event.status]} size={13} /></span>
          <div className="feed-body">
            <div className="feed-line">
              <span className="muted">{eventVerb[event.status]}</span> <strong>{event.name || event.recipient}</strong>
            </div>
            <div className="feed-meta">
              {event.campaignId ? <Link href={`/campaigns/view?id=${event.campaignId}`}>{event.campaignName}</Link> : <span>One-off email</span>}
              {event.attempts > 1 && <span> · attempt {event.attempts}</span>}
              {event.error && (event.status === "failed" || event.status === "retrying") && <span className="feed-error" title={event.error}> · {event.error}</span>}
            </div>
          </div>
          <time>{timeAgo(event.at, now)}</time>
        </li>
      ))}
    </ul>
  );
}

export function LiveLabel({ updatedAt, now, error }: { updatedAt: number | null; now: number; error?: Error | null }) {
  if (error) return <span className="live-label is-error"><Icon name="alert" size={13} /> Connection lost, retrying…</span>;
  return (
    <span className="live-label"><LiveDot /> Live{updatedAt ? ` · updated ${timeAgo(new Date(updatedAt).toISOString(), now)}` : ""}</span>
  );
}
