// The desktop window serves the UI and the API from one origin.
const TOKEN_KEY = "session";

/** The admin session token, kept for the life of the window. */
export const sessionToken = {
  get: () => { try { return sessionStorage.getItem(TOKEN_KEY); } catch { return null; } },
  set: (token: string) => { try { sessionStorage.setItem(TOKEN_KEY, token); } catch { /* private mode */ } },
  clear: () => { try { sessionStorage.removeItem(TOKEN_KEY); } catch { /* private mode */ } },
};

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

/**
 * Calls the API with the session token. A 401 sends the user back to /login,
 * and a 503 from an app that is not connected yet sends them to /setup.
 */
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const token = sessionToken.get();
  if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
  let response: Response;
  try {
    response = await fetch(path, { ...init, headers });
  } catch {
    throw new ApiError(0, "The app could not reach its backend. Restart the app.");
  }
  const contentType = response.headers.get("content-type") ?? "";
  const payload = contentType.includes("application/json") ? await response.json() : await response.blob();
  if (response.status === 503 && (payload as { setup?: string })?.setup && window.location.pathname !== "/setup") {
    window.location.replace("/setup");
  }
  if (response.status === 401 && !path.startsWith("/api/v1/auth/") && !path.startsWith("/api/v1/setup")) {
    sessionToken.clear();
    window.dispatchEvent(new Event("session-expired"));
  }
  if (!response.ok) {
    const error = (payload as { error?: unknown })?.error;
    const message = typeof error === "string" ? error : error ? Object.values(error as Record<string, string>).join(", ") : `Request failed (${response.status})`;
    throw new ApiError(response.status, message);
  }
  return payload as T;
}

export const json = (body: unknown) => JSON.stringify(body);

export type CampaignStatus = "draft" | "running" | "paused" | "completed";

export type Campaign = {
  id: number; name: string; subject: string; body: string; sender: string; status: CampaignStatus;
  totalCount: number; validCount: number; sentCount: number; failedCount: number; batchSize: number;
  createdAt: string; startedAt: string | null; completedAt: string | null;
  breakdown?: Record<string, number>;
};

export type Recipient = { id: number; studentName: string; certificateNumber: string; email: string; status: string; errorMessage?: string };

export type PageMeta = { page: number; pageSize: number; total: number };

export type DeliveryEvent = {
  id: number; topic: string; status: "sent" | "failed" | "sending" | "retrying"; recipient: string; name: string;
  campaignId: number | null; campaignName: string; attempts: number; error?: string; at: string;
};

export type DeliveryStatus = {
  queue: { ready: number; scheduled: number; published: number; sending: number };
  today: { sent: number; failed: number };
  throughput: { minute: string; sent: number; failed: number }[];
  recent: DeliveryEvent[];
  nextRetry: string | null;
  workers: { online: number; perReplica: number };
  limits: { ratePerSecond: number; dailyLimit: number; dailyUsed: number; maxAttempts: number };
  smtpConfigured: boolean;
  serverTime: string;
};

export type Settings = {
  smtp: { host: string; port: number; username: string; sender: string; configured: boolean };
  certificate: { template: string; nameX: number; nameY: number; nameSize: number; numberX: number; numberY: number; numberSize: number };
  delivery: { workersPerReplica: number; ratePerSecond: number; dailyLimit: number; maxAttempts: number };
};
