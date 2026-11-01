"use client";

import { useMemo, useRef, useState } from "react";
import { Audience, AudienceRow, Mapping, RowStatus } from "@/lib/csv";
import { formatNumber } from "@/lib/format";
import { Icon, IconName } from "./Icon";

type Filter = "all" | "ready" | RowStatus;

const ROW_HEIGHT = 41;
const VIEWPORT = 440;
const OVERSCAN = 8;

const statusMeta: Record<RowStatus, { label: string; icon: IconName }> = {
  valid: { label: "Ready", icon: "check" },
  warning: { label: "Check", icon: "alert" },
  duplicate: { label: "Duplicate", icon: "users" },
  invalid: { label: "Invalid", icon: "x" },
};

export function AudiencePreview({ audience, onMapping, onFix }: {
  audience: Audience;
  onMapping: (mapping: Mapping) => void;
  onFix: (fixes: Record<number, string>) => void;
}) {
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const { headers, mapping, counts } = audience;
  const suggestions = audience.rows.filter(row => row.suggestion);

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return audience.rows.filter(row =>
      (filter === "all" || (filter === "ready" ? row.status === "valid" || row.status === "warning" : row.status === filter)) &&
      (!q || row.cells.some(cell => cell.toLowerCase().includes(q)) || row.email.includes(q)));
  }, [audience.rows, filter, query]);

  const stats: { key: Filter; label: string; value: number; tone: string; hint: string }[] = [
    { key: "ready", label: "Ready to send", value: counts.valid + counts.warning, tone: "green", hint: counts.warning ? `incl. ${counts.warning} to double-check` : "will receive a certificate" },
    { key: "duplicate", label: "Duplicates", value: counts.duplicate, tone: counts.duplicate ? "amber" : "", hint: `${audience.duplicates.length} repeated address${audience.duplicates.length === 1 ? "" : "es"} · first kept` },
    { key: "invalid", label: "Invalid", value: counts.invalid, tone: counts.invalid ? "red" : "", hint: "skipped" },
    { key: "warning", label: "Possible typos", value: counts.warning, tone: counts.warning ? "amber" : "", hint: "valid, but likely misspelled" },
  ];

  return (
    <div className="audience">
      <section className="panel">
        <div className="panel-head">
          <div>
            <h2>Columns</h2>
            <p className="muted small">{headers.length} columns detected. Choose which ones hold the name and the email; other columns are ignored.</p>
          </div>
        </div>
        <div className="mapping">
          <MappingSelect label="Student name" icon="users" value={mapping.name} headers={headers} onChange={name => onMapping({ ...mapping, name })} />
          <MappingSelect label="Email address" icon="mail" value={mapping.email} headers={headers} onChange={email => onMapping({ ...mapping, email })} />
          <div className="column-chips">
            {headers.map((header, index) => (
              <span key={index} className={`column-chip ${index === mapping.name ? "is-name" : index === mapping.email ? "is-email" : ""}`}>
                {header || `Column ${index + 1}`}
                <em>{index === mapping.name ? "Name" : index === mapping.email ? "Email" : "Not used"}</em>
              </span>
            ))}
          </div>
        </div>
      </section>

      {audience.error ? (
        <div className="banner banner-error"><Icon name="alert" size={18} /><div><strong>Can&apos;t read recipients yet.</strong> {audience.error}</div></div>
      ) : <>
        <div className="audience-stats four">
          {stats.map(stat => (
            <button key={stat.key} className={`stat ${stat.tone ? `stat-${stat.tone}` : ""} ${filter === stat.key ? "is-active" : ""}`}
              onClick={() => setFilter(filter === stat.key ? "all" : stat.key)}>
              <span>{stat.label}</span>
              <strong className="tabular">{formatNumber(stat.value)}</strong>
              <small>{stat.hint}</small>
            </button>
          ))}
        </div>

        {audience.duplicates.length > 0 && <DuplicateGroups groups={audience.duplicates} />}

        <section className="panel flush">
          <div className="table-toolbar">
            <div className="segmented-control">
              {([["all", "All", audience.rows.length], ["ready", "Ready", counts.valid + counts.warning], ["warning", "Typos", counts.warning],
                ["duplicate", "Duplicates", counts.duplicate], ["invalid", "Invalid", counts.invalid]] as const).map(([key, label, count]) => (
                <button key={key} className={filter === key ? "is-active" : ""} onClick={() => setFilter(key)}>
                  {label}<span className="count">{formatNumber(count)}</span>
                </button>
              ))}
            </div>
            <div className="toolbar-right">
              {suggestions.length > 0 && (
                <button className="btn btn-secondary" onClick={() => onFix(Object.fromEntries(suggestions.map(row => [row.line, row.suggestion!])))}>
                  <Icon name="zap" size={15} /><span>Apply {suggestions.length} suggestion{suggestions.length === 1 ? "" : "s"}</span>
                </button>
              )}
              <label className="search">
                <Icon name="search" size={16} />
                <input placeholder="Search any column" value={query} onChange={e => setQuery(e.target.value)} />
              </label>
            </div>
          </div>
          <RowsTable headers={headers} mapping={mapping} rows={rows} onFix={(line, email) => onFix({ [line]: email })} />
          <div className="table-foot muted small">
            Showing {formatNumber(rows.length)} of {formatNumber(audience.rows.length)} rows · only “Ready” rows are imported
          </div>
        </section>
      </>}
    </div>
  );
}

function MappingSelect({ label, icon, value, headers, onChange }: { label: string; icon: IconName; value: number; headers: string[]; onChange: (index: number) => void }) {
  return (
    <label className={`mapping-select ${value < 0 ? "is-missing" : ""}`}>
      <span><Icon name={icon} size={14} /> {label}</span>
      <select value={value} onChange={e => onChange(Number(e.target.value))}>
        <option value={-1}>Choose a column…</option>
        {headers.map((header, index) => <option key={index} value={index}>{header || `Column ${index + 1}`}</option>)}
      </select>
    </label>
  );
}

function DuplicateGroups({ groups }: { groups: Audience["duplicates"] }) {
  const [expanded, setExpanded] = useState(false);
  const visible = expanded ? groups : groups.slice(0, 4);
  return (
    <section className="panel duplicates">
      <div className="panel-head">
        <div>
          <h2>Duplicate emails</h2>
          <p className="muted small">Each address gets one certificate. The first row is kept and later rows are skipped.</p>
        </div>
      </div>
      <ul>
        {visible.map(group => (
          <li key={group.email}>
            <div className="dup-email"><Icon name="mail" size={14} /><strong>{group.email}</strong>{group.conflictingNames && <span className="pill pill-amber">Different names</span>}</div>
            <div className="dup-rows">
              {group.lines.map((line, i) => (
                <span key={line} className={i === 0 ? "is-kept" : ""} title={group.names[i]}>
                  Row {line}{i === 0 ? " · kept" : ""}<em>{group.names[i] || "no name"}</em>
                </span>
              ))}
            </div>
          </li>
        ))}
      </ul>
      {groups.length > 4 && <button className="show-more" onClick={() => setExpanded(v => !v)}>{expanded ? "Show fewer" : `Show all ${groups.length}`}</button>}
    </section>
  );
}

/** Windowed table: only rows in view are rendered, so 50,000-row files stay smooth. */
function RowsTable({ headers, mapping, rows, onFix }: { headers: string[]; mapping: Mapping; rows: AudienceRow[]; onFix: (line: number, email: string) => void }) {
  const [scrollTop, setScrollTop] = useState(0);
  const box = useRef<HTMLDivElement>(null);
  const start = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN);
  const end = Math.min(rows.length, Math.ceil((scrollTop + VIEWPORT) / ROW_HEIGHT) + OVERSCAN);
  const columns = headers.length + 3;

  if (rows.length === 0) return <div className="rows-empty muted">No rows match this filter.</div>;
  return (
    <div className="rows-scroll" ref={box} style={{ maxHeight: VIEWPORT }} onScroll={e => setScrollTop(e.currentTarget.scrollTop)}>
      <table className="rows-table">
        <thead>
          <tr>
            <th className="col-line">Row</th>
            <th className="col-status">Status</th>
            <th className="col-issue">Issue</th>
            {headers.map((header, index) => (
              <th key={index} className={index === mapping.name || index === mapping.email ? "is-mapped" : ""}>
                {header || `Column ${index + 1}`}
                {index === mapping.name && <em className="is-name">Name</em>}
                {index === mapping.email && <em>Email</em>}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {start > 0 && <tr style={{ height: start * ROW_HEIGHT }}><td colSpan={columns} /></tr>}
          {rows.slice(start, end).map(row => (
            <tr key={row.line} className={`row-${row.status}`} style={{ height: ROW_HEIGHT }}>
              <td className="col-line tabular">{row.line}</td>
              <td className="col-status"><span className={`row-badge row-badge-${row.status}`}><Icon name={statusMeta[row.status].icon} size={11} />{statusMeta[row.status].label}</span></td>
              <td className="col-issue">
                {row.message ? <span className={`issue issue-${row.status}`}>{row.message}</span> : <span className="muted">—</span>}
                {row.suggestion && <button className="fix" onClick={() => onFix(row.line, row.suggestion!)}>Use {row.suggestion.split("@")[1]}</button>}
              </td>
              {headers.map((_, index) => (
                <td key={index} className={index === mapping.email && row.cells[index]?.trim().toLowerCase() !== row.email ? "is-corrected" : ""}>
                  {index === mapping.email && row.cells[index]?.trim().toLowerCase() !== row.email
                    ? <><s>{row.cells[index]}</s> {row.email}</>
                    : row.cells[index] ?? <span className="muted">—</span>}
                </td>
              ))}
            </tr>
          ))}
          {end < rows.length && <tr style={{ height: (rows.length - end) * ROW_HEIGHT }}><td colSpan={columns} /></tr>}
        </tbody>
      </table>
    </div>
  );
}
