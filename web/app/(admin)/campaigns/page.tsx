"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { CampaignProgress, percentDone } from "@/components/CampaignProgress";
import { Icon } from "@/components/Icon";
import { EmptyState, Skeleton, StatusBadge } from "@/components/ui";
import { api, Campaign, PageMeta } from "@/lib/api";
import { formatDate } from "@/lib/format";
import { usePoll } from "@/lib/usePoll";

const filters = [
  { key: "all", label: "All" },
  { key: "running", label: "Sending" },
  { key: "paused", label: "Paused" },
  { key: "completed", label: "Completed" },
  { key: "draft", label: "Drafts" },
] as const;

const PAGE_SIZE = 50;

export default function CampaignsPage() {
  const router = useRouter();
  const [page, setPage] = useState(1);
  const [filter, setFilter] = useState<(typeof filters)[number]["key"]>("all");
  const [query, setQuery] = useState("");
  const { data, loading } = usePoll(
    () => api<{ campaigns: Campaign[]; metadata: PageMeta }>(`/api/v1/campaigns?page=${page}&page_size=${PAGE_SIZE}`),
    5000, [page]);

  const campaigns = data?.campaigns ?? [];
  const counts = useMemo(() => {
    const result: Record<string, number> = { all: campaigns.length };
    for (const c of campaigns) result[c.status] = (result[c.status] ?? 0) + 1;
    return result;
  }, [campaigns]);
  const visible = campaigns.filter(c =>
    (filter === "all" || c.status === filter) &&
    (!query || `${c.name} ${c.subject}`.toLowerCase().includes(query.toLowerCase())));
  const pages = data ? Math.ceil(data.metadata.total / PAGE_SIZE) : 1;

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <div className="eyebrow">CAMPAIGNS</div>
          <h1>Campaigns</h1>
        </div>
        <Link className="btn btn-primary" href="/campaigns/new"><Icon name="plus" size={16} /><span>New campaign</span></Link>
      </header>

      <div className="toolbar">
        <div className="segmented-control">
          {filters.map(item => (
            <button key={item.key} className={filter === item.key ? "is-active" : ""} onClick={() => setFilter(item.key)}>
              {item.label}{counts[item.key] ? <span className="count">{counts[item.key]}</span> : null}
            </button>
          ))}
        </div>
        <label className="search">
          <Icon name="search" size={16} />
          <input placeholder="Search campaigns" value={query} onChange={e => setQuery(e.target.value)} />
        </label>
      </div>

      <section className="panel flush">
        {loading ? <TableSkeleton /> : campaigns.length === 0 ? (
          <EmptyState icon="campaigns" title="No campaigns yet"
            action={<Link className="btn btn-primary" href="/campaigns/new"><Icon name="plus" size={16} /><span>Create your first campaign</span></Link>}>
            Upload a CSV of students, write a message, and certificates are generated and emailed for you.
          </EmptyState>
        ) : visible.length === 0 ? (
          <EmptyState icon="search" title="No campaigns match">Try a different search or filter.</EmptyState>
        ) : (
          <table className="table is-clickable">
            <thead>
              <tr><th>Campaign</th><th>Status</th><th style={{ width: "30%" }}>Progress</th><th className="num">Delivered</th><th className="num">Failed</th><th>Created</th><th /></tr>
            </thead>
            <tbody>
              {visible.map(c => (
                <tr key={c.id} onClick={() => router.push(`/campaigns/view?id=${c.id}`)}>
                  <td><strong>{c.name}</strong><small>{c.subject}</small></td>
                  <td><StatusBadge status={c.status} /></td>
                  <td>
                    {c.status === "draft" ? <span className="muted small">{c.validCount} recipients ready</span> : <>
                      <CampaignProgress campaign={c} />
                      <small>{percentDone(c)}% processed</small>
                    </>}
                  </td>
                  <td className="num tabular">{c.sentCount} <span className="muted">/ {c.validCount}</span></td>
                  <td className={`num tabular ${c.failedCount ? "text-red" : "muted"}`}>{c.failedCount || "—"}</td>
                  <td className="muted">{formatDate(c.createdAt)}</td>
                  <td className="row-arrow"><Icon name="arrow" size={16} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {pages > 1 && (
        <div className="pager">
          <button className="btn btn-ghost" disabled={page <= 1} onClick={() => setPage(p => p - 1)}>Previous</button>
          <span className="muted small">Page {page} of {pages}</span>
          <button className="btn btn-ghost" disabled={page >= pages} onClick={() => setPage(p => p + 1)}>Next</button>
        </div>
      )}
    </div>
  );
}

function TableSkeleton() {
  return (
    <div className="table-skeleton">
      {Array.from({ length: 6 }, (_, i) => (
        <div key={i} className="skeleton-line">
          <div className="stack-tight" style={{ width: "28%" }}><Skeleton width="80%" /><Skeleton width="55%" height={10} /></div>
          <Skeleton width={74} height={22} radius={11} />
          <Skeleton width="26%" height={8} radius={4} />
          <Skeleton width={60} />
          <Skeleton width={90} />
        </div>
      ))}
    </div>
  );
}
