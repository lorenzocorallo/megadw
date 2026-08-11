export type AuthStatus = {
  setupRequired: boolean;
  authenticated: boolean;
  user?: { id: string; username: string };
};

export type Settings = {
  paths: {
    incompleteRoot: string;
    completeRoot: string;
  };
  downloads: {
    autoStart: boolean;
    segmentSizeBytes: number;
    workersPerFile: number;
    maxActiveFiles: number;
    maxGlobalWorkers: number;
    globalSpeedLimitBytesPerSecond: number;
    perJobDefaultLimitBytesPerSecond: number;
    conflictPolicy: "rename" | "overwrite" | "fail";
    checkpointIntervalMs: number;
    checkpointBytes: number;
    normalRetryLimit: number;
  };
  network: {
    connectTimeoutSeconds: number;
    responseHeaderTimeoutSeconds: number;
    readIdleTimeoutSeconds: number;
  };
  ui: {
    theme: "light" | "dark" | "system";
    locale: "en";
  };
};

export type ResolvedFile = {
  nodeId: string;
  relativePath: string;
  size: number;
};

export type ResolvedDownload = {
  kind: "file" | "folder";
  displayName: string;
  totalBytes: number;
  fileCount: number;
  files: ResolvedFile[];
};

export type Download = {
  id: string;
  sourceKind: "file" | "folder";
  displayName: string;
  totalBytes: number;
  accountId?: string;
  proxyId?: string;
  destinationSubdirectory: string;
  completeRoot: string;
  incompleteRoot: string;
  state: string;
  createdAt: string;
  updatedAt: string;
  quotaNextRetryAt?: string;
  quotaRetryIndex?: number;
  lastErrorCode?: string;
  lastErrorMessage?: string;
  bytesCommitted: number;
  speedBytesPerSecond: number;
  etaSeconds?: number;
  accountLabel?: string;
  proxyLabel?: string;
  files: Array<{
    id: string;
    finalRelativePath: string;
    sizeBytes: number;
    bytesCommitted: number;
    state: string;
    updatedAt?: string;
  }>;
  events?: DownloadEvent[];
};

export type DownloadEvent = {
  id: number;
  jobId: string;
  fileId?: string;
  kind: string;
  message: string;
  createdAt: string;
};

export type Dashboard = {
  activeJobs: number;
  queuedJobs: number;
  waitingQuotaJobs: number;
  currentSpeedBytesPerSecond: number;
  bytesDownloadedThisSession: number;
  diskFreeBytes: number;
};

export type Account = {
  id: string;
  label: string;
  email: string;
  status: string;
  defaultForDownloads: boolean;
  lastCheckedAt?: string;
};
export type ProxyProfile = {
  id: string;
  name: string;
  type: string;
  host: string;
  port: number;
  username?: string;
  timeoutSeconds: number;
  enabled: boolean;
  defaultForDownloads: boolean;
};

type Envelope<T> = { data: T };
type ErrorEnvelope = { error?: { code?: string; message?: string; details?: unknown } };

export class APIError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "APIError";
    this.code = code;
  }
}

export async function apiRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body) headers.set("Content-Type", "application/json");
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers,
  });
  const body = (await response.json().catch(() => null)) as (Envelope<T> & ErrorEnvelope) | null;
  if (!response.ok) {
    throw new APIError(
      body?.error?.code ?? "request_failed",
      body?.error?.message ?? response.statusText,
    );
  }
  return body && "data" in body ? body.data : (undefined as T);
}

export function getAuthStatus(): Promise<AuthStatus> {
  return apiRequest<AuthStatus>("/api/v1/auth/status");
}

export function setupAdmin(username: string, password: string) {
  return apiRequest<{ id: string; username: string }>("/api/v1/auth/setup", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
}

export function login(username: string, password: string) {
  return apiRequest<{ id: string; username: string }>("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
}

export function logout() {
  return apiRequest<{ loggedOut: boolean }>("/api/v1/auth/logout", { method: "POST" });
}

export function getSettings() {
  return apiRequest<Settings>("/api/v1/settings");
}

export function putSettings(value: Settings) {
  return apiRequest<Settings>("/api/v1/settings", {
    method: "PUT",
    body: JSON.stringify(value),
  });
}

export function resolveDownload(url: string, accountId = "", proxyId = "") {
  return apiRequest<ResolvedDownload>("/api/v1/downloads/resolve", {
    method: "POST",
    body: JSON.stringify({ url, accountId, proxyId }),
  });
}

export function createDownload(input: {
  url: string;
  accountId?: string;
  proxyId?: string;
  destinationSubdirectory: string;
  startImmediately: boolean;
}) {
  return apiRequest<Download>("/api/v1/downloads", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function listDownloads() {
  return apiRequest<Download[]>("/api/v1/downloads");
}
export function getDownload(id: string) {
  return apiRequest<Download>(`/api/v1/downloads/${id}`);
}
export function listDownloadEvents(id: string) {
  return apiRequest<DownloadEvent[]>(`/api/v1/downloads/${id}/events`);
}
export function resumeDownload(id: string) {
  return apiRequest<Download>(`/api/v1/downloads/${id}/resume`, { method: "POST", body: "{}" });
}
export function pauseDownload(id: string) {
  return apiRequest<Download>(`/api/v1/downloads/${id}/pause`, { method: "POST", body: "{}" });
}
export function retryDownload(id: string) {
  return apiRequest<Download>(`/api/v1/downloads/${id}/retry`, { method: "POST", body: "{}" });
}
export function cancelDownload(id: string, deletePartialFiles = false) {
  return apiRequest<Download>(`/api/v1/downloads/${id}/cancel`, {
    method: "POST",
    body: JSON.stringify({ deletePartialFiles }),
  });
}
export function deleteDownload(id: string, deleteFiles: boolean) {
  return apiRequest<{ deleted: boolean }>(
    `/api/v1/downloads/${id}?deleteFiles=${String(deleteFiles)}`,
    { method: "DELETE" },
  );
}
export function pauseQueue() {
  return apiRequest<{ paused: boolean }>("/api/v1/queue/pause", { method: "POST", body: "{}" });
}
export function resumeQueue() {
  return apiRequest<{ paused: boolean }>("/api/v1/queue/resume", { method: "POST", body: "{}" });
}
export function getDashboard() {
  return apiRequest<Dashboard>("/api/v1/dashboard");
}

export function listAccounts() {
  return apiRequest<Account[]>("/api/v1/accounts");
}
export function createAccount(input: {
  label: string;
  email: string;
  password: string;
  defaultForDownloads: boolean;
}) {
  return apiRequest<Account>("/api/v1/accounts", { method: "POST", body: JSON.stringify(input) });
}
export function testAccount(id: string) {
  return apiRequest<{ status: string }>(`/api/v1/accounts/${id}/test`, {
    method: "POST",
    body: "{}",
  });
}
export function deleteAccount(id: string) {
  return apiRequest<{ deleted: boolean }>(`/api/v1/accounts/${id}`, { method: "DELETE" });
}
export function updateAccount(
  id: string,
  input: Partial<{ label: string; email: string; password: string; defaultForDownloads: boolean }>,
) {
  return apiRequest<Account>(`/api/v1/accounts/${id}`, {
    method: "PUT",
    body: JSON.stringify(input),
  });
}
export function listProxies() {
  return apiRequest<ProxyProfile[]>("/api/v1/proxies");
}
export function createProxy(input: {
  name: string;
  type: string;
  host: string;
  port: number;
  username: string;
  password: string;
  timeoutSeconds: number;
  enabled: boolean;
  defaultForDownloads: boolean;
}) {
  return apiRequest<ProxyProfile>("/api/v1/proxies", {
    method: "POST",
    body: JSON.stringify(input),
  });
}
export function testProxy(id: string) {
  return apiRequest<{ ok: boolean }>(`/api/v1/proxies/${id}/test`, { method: "POST", body: "{}" });
}
export function deleteProxy(id: string) {
  return apiRequest<{ deleted: boolean }>(`/api/v1/proxies/${id}`, { method: "DELETE" });
}
export function updateProxy(
  id: string,
  input: Partial<{
    name: string;
    type: string;
    host: string;
    port: number;
    username: string;
    password: string;
    timeoutSeconds: number;
    enabled: boolean;
    defaultForDownloads: boolean;
  }>,
) {
  return apiRequest<ProxyProfile>(`/api/v1/proxies/${id}`, {
    method: "PUT",
    body: JSON.stringify(input),
  });
}
