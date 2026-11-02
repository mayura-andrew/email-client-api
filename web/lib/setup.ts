import { api, ApiError, json, sessionToken } from "./api";

export type SetupState = "setup" | "starting" | "running" | "error";

export type SetupStatus = {
  state: SetupState; error: string; needsConnections: boolean; needsAdmin: boolean;
  databaseUrl: string; redisUrl: string;
};

export type Connections = {
  databaseUrl: string; redisUrl: string; adminEmail: string; secret: string;
  smtpRatePerSecond: number; smtpDailyLimit: number; configDir: string;
};

export type SetupRequest = Partial<{
  databaseUrl: string; redisUrl: string; adminEmail: string; adminPassword: string; currentPassword: string;
  secret: string; smtpRatePerSecond: number; smtpDailyLimit: number;
}>;

export const setupStatus = () => api<SetupStatus>("/api/v1/setup/status");

export const testConnections = (body: SetupRequest) =>
  api<{ databaseUrl: string; redisUrl: string }>("/api/v1/setup/test", { method: "POST", body: json(body) });

/** Field errors from a rejected save, keyed like SetupRequest. */
export class SetupError extends Error {
  constructor(public fields: Record<string, string>) { super(Object.values(fields).join(" ")); }
}

/** Saves the setup; the app then reconnects in the background. */
export async function saveSetup(body: SetupRequest): Promise<void> {
  const response = await fetch("/api/v1/setup", {
    method: "POST", body: json(body),
    headers: { "Content-Type": "application/json", ...(sessionToken.get() ? { Authorization: `Bearer ${sessionToken.get()}` } : {}) },
  });
  if (response.ok) return;
  const payload = await response.json().catch(() => ({}));
  if (payload.error && typeof payload.error === "object") throw new SetupError(payload.error);
  throw new ApiError(response.status, payload.error ?? `Request failed (${response.status})`);
}

/** Polls until the app has connected (or failed to), after a save or retry. */
export async function waitUntilStarted(): Promise<SetupStatus> {
  for (;;) {
    const status = await setupStatus();
    if (status.state !== "starting") return status;
    await new Promise(resolve => setTimeout(resolve, 800));
  }
}
