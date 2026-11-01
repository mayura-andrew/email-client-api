import { DeliveryStatus } from "./api";

/**
 * Sends per second to plan with: what was actually observed over the last
 * five minutes, falling back to the configured limit before anything is sent.
 */
export function effectiveRate(status: DeliveryStatus | null): number {
  if (!status) return 0;
  // Measure from the first active minute so idle time before a campaign
  // started doesn't drag the rate down. The current minute is still filling
  // up, so it only counts once something else is in the window.
  const recent = status.throughput.slice(-6);
  const first = recent.findIndex(point => point.sent > 0);
  const active = first < 0 ? [] : recent.slice(first, recent.length - 1);
  const observed = active.reduce((sum, point) => sum + point.sent, 0) / (active.length * 60 || 1);
  if (observed > 0) return observed;
  return status.limits.ratePerSecond;
}

/** Seconds until `remaining` messages are sent, or Infinity when unknown/unlimited. */
export function etaSeconds(remaining: number, status: DeliveryStatus | null): number {
  const rate = effectiveRate(status);
  if (remaining <= 0) return 0;
  if (!rate) return Infinity;
  return remaining / rate;
}
