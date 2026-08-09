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
  state: string;
  createdAt: string;
  updatedAt: string;
  files: Array<{
    id: string;
    finalRelativePath: string;
    sizeBytes: number;
    state: string;
  }>;
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

export function resolveDownload(url: string) {
  return apiRequest<ResolvedDownload>("/api/v1/downloads/resolve", {
    method: "POST",
    body: JSON.stringify({ url, accountId: "" }),
  });
}

export function createDownload(input: {
  url: string;
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
