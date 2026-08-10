import type { Download } from "@/api/client";

export type StreamEvent = {
  name: string;
  jobId?: string;
  fileId?: string;
  timestamp?: string;
  data?: Record<string, unknown>;
};

export function mergeDownloadSnapshot(
  current: Download,
  event: StreamEvent,
  payload: Record<string, unknown>,
): Download {
  const next = { ...current };
  const state = stringValue(payload.state);
  if (state && event.name === "job.updated") next.state = state;
  const bytes = numberValue(payload.bytesCommitted);
  if (bytes !== undefined && !event.fileId) next.bytesCommitted = bytes;
  const speed = numberValue(payload.speedBytesPerSecond);
  if (speed !== undefined) next.speedBytesPerSecond = speed;
  const eta = numberValue(payload.etaSeconds);
  if (eta !== undefined) next.etaSeconds = eta;
  const errorCode = stringValue(payload.lastErrorCode);
  if (errorCode !== undefined) next.lastErrorCode = errorCode;
  const errorMessage = stringValue(payload.lastErrorMessage);
  if (errorMessage !== undefined) next.lastErrorMessage = errorMessage;
  if (Object.hasOwn(payload, "quotaNextRetryAt")) {
    next.quotaNextRetryAt = stringValue(payload.quotaNextRetryAt);
  }
  const quotaRetryIndex = numberValue(payload.quotaRetryIndex);
  if (quotaRetryIndex !== undefined) next.quotaRetryIndex = quotaRetryIndex;
  const updatedAt = stringValue(payload.updatedAt);
  if (updatedAt !== undefined) next.updatedAt = updatedAt;
  const files = payload.files;
  if (Array.isArray(files)) {
    next.files = current.files.map((file) => {
      const update = files.find((value) => isRecord(value) && value.id === file.id);
      return update && isRecord(update)
        ? {
            ...file,
            state: stringValue(update.state) ?? file.state,
            bytesCommitted: numberValue(update.bytesCommitted) ?? file.bytesCommitted,
            updatedAt: stringValue(update.updatedAt) ?? file.updatedAt,
          }
        : file;
    });
  }
  if (event.fileId) {
    next.files = next.files.map((file) =>
      file.id === event.fileId
        ? {
            ...file,
            state: state ?? file.state,
            bytesCommitted: bytes ?? file.bytesCommitted,
          }
        : file,
    );
    next.bytesCommitted = next.files.reduce((total, file) => total + file.bytesCommitted, 0);
  }
  return next;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}
