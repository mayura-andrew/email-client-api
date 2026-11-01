"use client";

import { ReactNode, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Icon, IconName } from "./Icon";

/** Tweens from the previous value to the new one so live counters feel continuous. */
export function AnimatedNumber({ value, format = (n: number) => Math.round(n).toLocaleString("en-US") }: { value: number; format?: (n: number) => string }) {
  const [display, setDisplay] = useState(value);
  const from = useRef(value);
  useEffect(() => {
    const start = performance.now();
    const initial = from.current;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduce || initial === value) { setDisplay(value); from.current = value; return; }
    let frame = 0;
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / 700);
      const eased = 1 - Math.pow(1 - t, 3);
      const next = initial + (value - initial) * eased;
      setDisplay(next);
      from.current = next;
      if (t < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [value]);
  return <span className="tabular">{format(display)}</span>;
}

export function Spinner({ size = 16 }: { size?: number }) {
  return <span className="spinner" style={{ width: size, height: size }} aria-hidden="true" />;
}

export function Button({ children, loading, variant = "primary", icon, className = "", ...props }:
  React.ButtonHTMLAttributes<HTMLButtonElement> & { loading?: boolean; variant?: "primary" | "secondary" | "ghost" | "danger"; icon?: IconName }) {
  return (
    <button {...props} className={`btn btn-${variant} ${loading ? "is-loading" : ""} ${className}`} disabled={props.disabled || loading}>
      {loading ? <Spinner /> : icon ? <Icon name={icon} size={16} /> : null}
      <span>{children}</span>
    </button>
  );
}

export function Skeleton({ width = "100%", height = 14, radius = 6, className = "" }: { width?: number | string; height?: number; radius?: number; className?: string }) {
  return <span className={`skeleton ${className}`} style={{ width, height, borderRadius: radius }} />;
}

export function LiveDot({ active = true }: { active?: boolean }) {
  return <span className={`live-dot ${active ? "is-active" : ""}`} aria-hidden="true" />;
}

const statusLabels: Record<string, string> = {
  valid: "Ready", queued: "Queued", sending: "Sending", sent: "Delivered", retrying: "Retrying", failed: "Failed",
  draft: "Draft", running: "Sending", paused: "Paused", completed: "Completed", imported: "Imported",
};

export function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`badge badge-${status}`}>
      <i />{statusLabels[status] ?? status}
    </span>
  );
}

export function EmptyState({ icon, title, children, action }: { icon: IconName; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty-state">
      <div className="empty-icon"><Icon name={icon} size={26} /></div>
      <h3>{title}</h3>
      {children && <p>{children}</p>}
      {action}
    </div>
  );
}

export function Modal({ open, onClose, children, width = 520 }: { open: boolean; onClose?: () => void; children: ReactNode; width?: number }) {
  useEffect(() => {
    if (!open || !onClose) return;
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);
  if (!open) return null;
  // Portalled to <body>: an animated (transformed) ancestor would otherwise
  // become the containing block and trap the fixed backdrop inside it.
  return createPortal(
    <div className="modal-backdrop" onMouseDown={event => { if (event.target === event.currentTarget) onClose?.(); }}>
      <div className="modal" role="dialog" aria-modal="true" style={{ width }}>{children}</div>
    </div>,
    document.body,
  );
}

/** One bar split into proportional, animated segments (delivered, sending, queued...). */
export function SegmentedProgress({ segments, total, height = 10, striped }: {
  segments: { key: string; value: number }[]; total: number; height?: number; striped?: boolean;
}) {
  return (
    <div className={`segmented ${striped ? "is-striped" : ""}`} style={{ height }} role="progressbar"
      aria-valuenow={segments.find(s => s.key === "sent")?.value ?? 0} aria-valuemax={total}>
      {segments.map(segment => (
        <span key={segment.key} className={`segment segment-${segment.key}`}
          style={{ width: `${total ? (segment.value / total) * 100 : 0}%` }} />
      ))}
    </div>
  );
}

export function Dropzone({ accept, onFile, children, compact }: { accept: string; onFile: (file: File) => void; children: ReactNode; compact?: boolean }) {
  const [dragging, setDragging] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  return (
    <div className={`dropzone ${dragging ? "is-dragging" : ""} ${compact ? "is-compact" : ""}`} role="button" tabIndex={0}
      onClick={() => input.current?.click()}
      onKeyDown={event => { if (event.key === "Enter" || event.key === " ") input.current?.click(); }}
      onDragOver={event => { event.preventDefault(); setDragging(true); }}
      onDragLeave={() => setDragging(false)}
      onDrop={event => { event.preventDefault(); setDragging(false); const file = event.dataTransfer.files?.[0]; if (file) onFile(file); }}>
      <input ref={input} type="file" accept={accept} hidden onChange={event => { const file = event.target.files?.[0]; if (file) onFile(file); event.target.value = ""; }} />
      {children}
    </div>
  );
}
