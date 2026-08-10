# Future Plan - MEGA Uploads

Status: deferred; do not execute during downloader MVP
Dependency: main `PLAN.md` must be complete, released, and stable first
Resource target: must coexist with the downloader inside the same 2 vCPU / 4 GiB minimum resource profile without unbounded worker, buffer, or queue growth

## Goal

Add efficient, resumable uploads to MEGA while reusing the existing account, proxy, settings, persistence, event, and web UI infrastructure.

## Product constraints

- Keep Go backend and CSR React frontend.
- Uploads require an authenticated MEGA account; anonymous upload is not supported.
- Reuse explicit proxy profiles. Do not create quota-evasion proxy rotation.
- Preserve low memory usage through streaming encryption.
- Do not stage a second full-size encrypted copy on disk.
- Upload state must survive application restart.
- Keep user-visible strings in the existing i18n resource system.
- Do not generalize the downloader into a generic transfer framework before upload implementation starts. Extract shared retry/limiter/telemetry primitives only where upload proves a second real consumer.
- Treat resumable upload semantics as protocol-dependent. Run a dedicated protocol spike before promising byte-accurate cross-process resume.
- Browser requests identify local sources as `allowedRootId + relativePath`; they never submit an arbitrary absolute filesystem path for execution.

## Backend additions

Create:

```text
internal/upload/
  manager.go
  scheduler.go
  job.go
  planner.go
  worker.go
  checkpoint.go
  integrity.go
```

Extend `internal/mega` only with upload-required protocol operations.

Add DB tables:

```text
upload_jobs
upload_files
```

Reuse encrypted `mega_accounts` and `proxy_profiles`.

## Upload model

Upload states:

```text
queued
preparing
uploading
paused
waiting_quota
finalizing
completed
failed
cancelled
```

Support:

- single local file;
- recursive local directory;
- remote target directory selection by MEGA node ID;
- conflict policies: rename, overwrite where protocol permits, skip;
- pause/resume/cancel;
- retry/backoff;
- bandwidth limiting;
- parallel chunk upload with global concurrency caps;
- safe default on the small profile: start with 2 workers per file and 4 global upload workers, then change defaults only if the 2-vCPU benchmark proves a throughput benefit;
- download and upload worker limits share an application-level network/CPU budget so enabling both cannot multiply concurrency without bound.

## Disk/memory rules

- Read source file directly.
- At planning time persist a source fingerprint sufficient to detect replacement/mutation across resume: root ID, relative path, device/inode where available, size, and mtime. Revalidate before resuming and before remote commit.
- Encrypt chunks as a stream.
- Do not create a second encrypted local file.
- Per-worker buffer target <= 1 MiB.
- Persist only protocol resume state and completed chunk metadata.
- If the MEGA upload protocol cannot resume an interrupted upload session safely, restart only the affected file and make this behavior explicit in UI.

## API additions

```text
POST   /api/v1/uploads/resolve-local   # { allowedRootId, relativePath }
POST   /api/v1/uploads
GET    /api/v1/uploads
GET    /api/v1/uploads/:id
POST   /api/v1/uploads/:id/pause
POST   /api/v1/uploads/:id/resume
POST   /api/v1/uploads/:id/retry
POST   /api/v1/uploads/:id/cancel
DELETE /api/v1/uploads/:id
GET    /api/v1/accounts/:id/upload-folders
```

Do not expose arbitrary filesystem browsing. Configure `uploadAllowedRoots` as server-side records with stable IDs, labels, and absolute roots. Runtime upload requests use only a root ID plus a relative path and are resolved through the same root-confined filesystem layer used by downloads.

`GET /api/v1/accounts/:id/upload-folders` is a folder-only destination picker: return only the MEGA folder tree needed to choose an upload target. It is not a general cloud-drive browser and exposes no file operations.

## UI additions

Routes:

```text
/uploads
/uploads/$uploadId
```

Add Upload dialog:

```text
allowed root + relative local path (never a free-form absolute path)
MEGA account
remote destination
proxy
start immediately
```

Downloads and uploads should eventually share a top-level `Transfers` navigation section but keep separate tables for the first upload release.

## Tests

Required before release:

- encrypted upload matches a fake-server reference vector;
- parallel and single-worker results match;
- restart behavior is deterministic;
- 429/5xx retry behavior;
- quota wait/resume behavior;
- proxy route test;
- source-file mutation during upload is detected and fails safely;
- no duplicate full-size temporary file is created;
- recursive folder upload preserves relative paths;
- cancellation releases file descriptors and workers;
- source fingerprint mismatch after pause/restart fails safely;
- folder-only remote picker never exposes file mutation operations;
- simultaneous download + upload remains inside the small-profile resource envelope;
- opt-in live upload compatibility smoke against a project-owned tiny fixture.

## Definition of done

- [ ] Authenticated file upload works.
- [ ] Directory upload works.
- [ ] Upload queue survives restart.
- [ ] Parallel uploads respect global worker limits.
- [ ] Bandwidth limit works.
- [ ] Proxy selection works.
- [ ] Quota errors wait/resume without identity rotation.
- [ ] No full-size staging copy is required.
- [ ] Browser UI controls every upload lifecycle action.
