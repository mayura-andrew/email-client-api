"use client";

import { useCallback, useEffect, useRef, useState } from "react";

/**
 * Loads data now and then every `interval` ms while the tab is visible.
 * Requests never overlap, and the returned `refresh` reloads on demand.
 */
export function usePoll<T>(load: () => Promise<T>, interval: number | null, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const inFlight = useRef(false);
  const loadRef = useRef(load);
  loadRef.current = load;

  const refresh = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      setData(await loadRef.current());
      setError(null);
      setUpdatedAt(Date.now());
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      inFlight.current = false;
    }
  }, []);

  useEffect(() => {
    refresh();
    if (!interval) return;
    const timer = window.setInterval(() => { if (!document.hidden) refresh(); }, interval);
    const onVisible = () => { if (!document.hidden) refresh(); };
    document.addEventListener("visibilitychange", onVisible);
    return () => { window.clearInterval(timer); document.removeEventListener("visibilitychange", onVisible); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [interval, refresh, ...deps]);

  return { data, error, updatedAt, refresh, loading: data === null && error === null };
}

/** Re-renders every `ms` so relative times ("12s ago") stay current. */
export function useNow(ms = 1000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(timer);
  }, [ms]);
  return now;
}
