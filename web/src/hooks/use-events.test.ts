import { describe, expect, it } from "vite-plus/test";
import type { Download } from "@/api/client";
import { mergeDownloadSnapshot } from "@/lib/download-events";

const download: Download = {
  id: "job-1",
  sourceKind: "file",
  displayName: "example.bin",
  totalBytes: 100,
  destinationSubdirectory: "",
  completeRoot: "/complete",
  incompleteRoot: "/incomplete",
  state: "downloading",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  bytesCommitted: 0,
  speedBytesPerSecond: 0,
  files: [
    {
      id: "file-1",
      finalRelativePath: "example.bin",
      sizeBytes: 100,
      bytesCommitted: 0,
      state: "downloading",
    },
  ],
};

describe("SSE download snapshot merging", () => {
  it("applies coalesced file progress without replacing server metadata", () => {
    const next = mergeDownloadSnapshot(
      download,
      { name: "speed.updated", jobId: "job-1", fileId: "file-1" },
      {
        bytesCommitted: 48,
        speedBytesPerSecond: 2048,
        state: "downloading",
      },
    );
    expect(next.displayName).toBe("example.bin");
    expect(next.bytesCommitted).toBe(48);
    expect(next.speedBytesPerSecond).toBe(2048);
    expect(next.files[0]?.bytesCommitted).toBe(48);
  });

  it("treats speed progress as file-scoped even after a job snapshot", () => {
    const folder: Download = {
      ...download,
      totalBytes: 200,
      files: [
        ...download.files,
        {
          id: "file-2",
          finalRelativePath: "second.bin",
          sizeBytes: 100,
          bytesCommitted: 0,
          state: "pending",
        },
      ],
    };
    const next = mergeDownloadSnapshot(
      folder,
      { name: "speed.updated", jobId: "job-1", fileId: "file-1" },
      {
        bytesCommitted: 48,
        state: "downloading",
        files: [{ id: "file-1", bytesCommitted: 0, state: "pending" }],
      },
    );
    expect(next.files[0]?.bytesCommitted).toBe(48);
    expect(next.files[1]?.bytesCommitted).toBe(0);
    expect(next.bytesCommitted).toBe(48);
  });

  it("keeps quota recovery metadata authoritative and clears it on resume", () => {
    const waiting = mergeDownloadSnapshot(
      download,
      { name: "job.updated", jobId: "job-1" },
      {
        state: "waiting_quota",
        quotaNextRetryAt: "2026-01-01T00:05:00Z",
        quotaRetryIndex: 2,
      },
    );
    expect(waiting.state).toBe("waiting_quota");
    expect(waiting.quotaNextRetryAt).toBe("2026-01-01T00:05:00Z");
    expect(waiting.quotaRetryIndex).toBe(2);

    const resumed = mergeDownloadSnapshot(
      waiting,
      { name: "job.updated", jobId: "job-1" },
      { state: "queued", quotaNextRetryAt: null, quotaRetryIndex: 0 },
    );
    expect(resumed.state).toBe("queued");
    expect(resumed.quotaNextRetryAt).toBeUndefined();
    expect(resumed.quotaRetryIndex).toBe(0);
  });
});
