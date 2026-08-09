# Development Plan - MEGA Downloader (Go + React)

Status: implementation-ready MVP plan, revised 2026-08-08
Primary scope: high-performance MEGA downloading only
Out of scope for this plan: upload, streaming, *arr integration, cloud file browser
Deployment target: Debian/Ubuntu LXC, systemd, single self-contained application process
Resource target: 2 vCPU / 4 GiB RAM minimum; 4 vCPU / 8 GiB RAM maximum target profile
Design priority: correctness and recoverability first, then measured throughput, then UI polish
Default download destination: `/mnt/media/downloads/mega/complete`

## 0. Non-negotiable product decisions

The implementation agents MUST follow these decisions. Do not substitute libraries, architectures, protocols, or UI frameworks unless a dependency is unavailable or broken at build time. If that happens, document the blocker in `BLOCKERS.md` and choose the smallest compatible replacement.

1. Backend: Go 1.26.x. Set `go 1.26.0` and `toolchain go1.26.5` in `go.mod` for the initial implementation.
2. Frontend: React + TypeScript 7.0.x, client-side rendered SPA. Pin the exact TypeScript release in the committed lockfile; do not silently fall back to TypeScript 6.
3. Frontend toolchain: Vite+ (`vite-plus`) using a published release only, with Node.js 24 LTS managed by Vite+. Vite+ is allowed while beta because it is build-time only; pin it exactly and keep a documented rollback path to plain Vite/Vitest/Oxlint/Oxfmt if a blocking regression appears.
4. Router: `@tanstack/react-router` with file-based routing. Do NOT use TanStack Start. The Go process is the application server.
5. Server state: `@tanstack/react-query`.
6. Download table: `@tanstack/react-table`.
7. Charts: `@tanstack/react-charts` only for the download-detail throughput chart. Keep it behind one local `ThroughputChart` adapter component because TanStack Charts is pre-1.0; chart breakage must never block downloads, routing, settings, or the rest of the UI. Do not add another chart library.
8. UI: Tailwind CSS + shadcn/ui + Lucide icons.
9. i18n: `i18next` + `react-i18next`. Ship only English strings in MVP, but no user-visible string may be hard-coded directly in page components.
10. Live updates: Server-Sent Events (SSE), not WebSockets.
11. Persistence: SQLite in WAL mode through `database/sql` using `modernc.org/sqlite` (pure Go; no CGO). Do not use an ORM.
12. HTTP server/router: standard-library `net/http` and Go 1.26 `http.ServeMux`. Do not add Gin, Fiber, Echo, Chi, or another HTTP framework unless this plan is explicitly revised.
13. HTTP API: standard JSON REST under `/api/v1`.
14. Logging: Go `log/slog`; human-readable text to stdout for systemd/journald. Do not store verbose progress logs in SQLite.
15. Packaging: one Go binary containing the built React assets with `go:embed`.
16. Linux service: systemd unit. Docker may be added later but is not required for MVP.
17. License: GPL-3.0 for the initial repository because MegaBasterd is GPL-3.0 and this project is explicitly a port/reference implementation. Keep third-party license notices. If a different license is desired later, perform a deliberate clean-room review first.
18. MEGA transfer limits: the application MUST be quota-aware and resume reliably after MEGA permits transfers again. It MUST NOT automatically rotate IPs, proxy identities, or accounts in response to quota errors for the purpose of bypassing MEGA transfer limits. Proxy support is for explicit network routing. HTTP 509 / over-quota states cause pause/backoff/retry, not identity rotation.
19. Default paths:
    - incomplete: `/mnt/media/downloads/mega/incomplete`
    - complete: `/mnt/media/downloads/mega/complete`
    - state: `/var/lib/megad`
    - database: `/var/lib/megad/megad.sqlite3`
20. Partial download storage: one sparse `.mega.part` file per remote file. Never create one temporary file per chunk. Never require a second full-size copy for merging.
21. A completed file is moved from `incomplete` to `complete` with an atomic rename. The two paths MUST be on the same filesystem; detect and warn if they are not.
22. Frontier dependency policy: use bleeding-edge technology only when its blast radius is bounded. Stable releases may be core dependencies; beta/pre-1.0 dependencies must be exact-pinned, isolated behind local boundaries, and replaceable without changing domain or API contracts.
23. Reproducibility: commit all lockfiles; CI installs frozen dependencies. Never build production artifacts from floating preview/commit builds.
24. Security maintenance: patch releases of Go/Node/frontend dependencies may be adopted after the full gate suite passes. Do not freeze known-vulnerable patch versions just to preserve byte-for-byte dependency age.
25. Do not introduce abstractions for upload or streaming inside the downloader until those plans are actually implemented. Share code only after a concrete second consumer exists.
26. The public MEGA-link protocol path is a release-critical compatibility risk. Validate it in an explicit early protocol spike before building the full application around any third-party library assumption.
27. Production runtime remains one Go process. Vite+, TypeScript, Node.js, test runners, and frontend package managers are build-time dependencies only.

## 1. Why this architecture

MegaBasterd provides the behavioral reference for the downloader: public MEGA links, account support, parallel chunk downloading, resume, retry logic, SQLite-backed state, proxy support, and explicit handling of quota-related HTTP 509 conditions. Do not port the Swing UI.

The existing Go library `github.com/t3rm1n4l/go-mega` is useful as a protocol and crypto foundation for authenticated account/tree/file operations and tested primitives. Do NOT assume it supplies the complete modern public-file/public-folder link flow. The application needs its own validated public-link resolver, payload-URL acquisition path where required, resumable range scheduler, persistence model, proxy-aware HTTP transports, progress events, and low-disk-overhead writer.

Use MegaBasterd as a behavioral/protocol reference and `go-mega` as a preferred source of reusable Go primitives only where project tests prove compatibility. Phase B below is a mandatory public-link/crypto compatibility spike; if it fails, fix or locally implement the smallest required protocol surface before continuing. Do not wrap or launch the Java application.

## 2. Repository layout

Create these package boundaries. The named top-level and `internal/` packages are architectural boundaries; individual `.go`/`.tsx` filenames may be merged or split when that improves cohesion. Do not create artificial one-type-per-file fragmentation just to mirror this list:

```text
/
  README.md
  LICENSE
  NOTICE.md
  PLAN.md
  PLAN-UPLOAD.md
  PLAN-STREAMING.md
  Makefile
  go.mod
  go.sum
  cmd/
    megad/
      main.go
  internal/
    api/
      router.go
      middleware.go
      auth_handlers.go
      download_handlers.go
      settings_handlers.go
      account_handlers.go
      proxy_handlers.go
      events_handler.go
      response.go
    app/
      app.go
      lifecycle.go
    auth/
      password.go
      session.go
    download/
      manager.go
      scheduler.go
      job.go
      file.go
      planner.go
      worker.go
      writer.go
      checkpoint.go
      retry.go
      speed.go
      limiter.go
      state.go
    mega/
      client.go
      links.go
      metadata.go
      folders.go
      accounts.go
      crypto.go
      integrity.go
      errors.go
      hashcash.go
    network/
      transport.go
      proxy.go
      throttled_reader.go
    fsroot/
      root.go
      paths.go
    settings/
      model.go
      service.go
      defaults.go
      validation.go
    store/
      db.go
      migrations.go
      downloads.go
      accounts.go
      proxies.go
      settings.go
      events.go
      secrets.go
    events/
      bus.go
      coalesce.go
      types.go
    webui/
      embed.go
      dist/
  migrations/
    001_initial.sql
  web/
    package.json
    vite.config.ts
    tsconfig.json
    components.json
    src/
      main.tsx
      routeTree.gen.ts
      routes/
      api/
      components/
      features/
      hooks/
      i18n/
        index.ts
        locales/en/common.json
      lib/
      styles/
  tests/
    integration/
      fake_mega_server.go
      fake_proxy.go
      fixtures/
  packaging/
    megad.service
    megad.env.example
```

Generated files may add to this tree. Keep the major package boundaries stable, but agents may refactor filenames inside a package when tests and imports remain coherent.

## 3. Java-to-Go port map

Agents port behavior, not class structure. Use this map to avoid architectural improvisation:

| MegaBasterd source | Go destination | Rule |
|---|---|---|
| `MegaAPI.java` | `internal/mega/client.go`, `metadata.go`, `accounts.go` | Port only APIs needed for download/link/account resolution. |
| `CryptTools.java` | `internal/mega/crypto.go`, `integrity.go` | Reuse tested MIT-licensed go-mega primitives when possible. Add deterministic test vectors. |
| `HashcashSolver.java` | `internal/mega/hashcash.go` | Implement only if the current MEGA API flow requires it. Prefer working go-mega implementation. |
| `DownloadManager.java` | `internal/download/manager.go`, `scheduler.go` | Global queue and concurrency ownership. |
| `Download.java` | `internal/download/job.go`, `file.go` | State machine and per-file lifecycle. |
| `ChunkDownloader.java` | `internal/download/worker.go` | HTTP range worker. |
| `ChunkDownloaderMono.java` | same worker with concurrency=1 | Do not create a separate implementation. |
| `ChunkWriterManager.java` | `internal/download/writer.go` | Replace temp-chunk merge design with direct `WriteAt` into one partial file. |
| `SmartMegaProxyManager.java` | `internal/network/proxy.go` | Port explicit proxy routing/health only. Do not port quota-evasion identity rotation. |
| `DBTools.java`, `SqliteSingleton.java` | `internal/store/*` | Typed repositories and migrations. |
| `AccountStore.java` | `internal/store/accounts.go`, `mega/accounts.go` | Encrypt credentials/session material at rest. |
| Swing UI classes | `web/` | Do not port. Rebuild as web UI. |
| Upload classes | deferred | See `PLAN-UPLOAD.md`. |
| Stream classes | deferred | See `PLAN-STREAMING.md`. |

## 3.1 Required Go dependencies

Keep the backend dependency surface intentionally small. The allowed baseline is:

- `github.com/t3rm1n4l/go-mega`, pinned by `go.sum`, used only for MEGA protocol/crypto/account primitives where behavior passes project tests. Public-link file/folder behavior is project-owned unless explicitly proven by tests. Do not delegate the transfer scheduler, public-folder resolver, quota policy, or proxy policy to it.
- `modernc.org/sqlite` through `database/sql` for persistence.
- `golang.org/x/crypto` for Argon2id and any missing cryptographic primitives not already in the standard library.
- `golang.org/x/net/proxy` only if SOCKS5 support cannot be implemented cleanly with standard `net/http` transport hooks.

Do not add an ORM, dependency-injection container, task-queue framework, generic retry package, or logging framework. Implement the small required abstractions locally.

## 4. Backend process model

The `megad` binary owns all application state.

Startup sequence:

1. Parse environment variables and CLI flags.
2. Create state directories if missing.
3. Open SQLite.
4. Enable WAL mode, foreign keys, and a busy timeout.
5. Run migrations.
6. Load settings and validate download paths.
7. Initialize secret encryption key.
8. Rehydrate downloads from SQLite.
9. Convert any persisted `downloading`, `resolving`, or `finalizing` state to `paused_recovery`.
10. Initialize account clients and proxy transports lazily, not all at once.
11. Start the download manager.
12. Start HTTP server.
13. Resume jobs marked `auto_resume=true` only after the HTTP server is healthy.

Shutdown sequence on SIGTERM/SIGINT:

1. Stop accepting new downloads.
2. Cancel active network requests.
3. Run a file checkpoint for all active files.
4. Persist job/file state.
5. Close SSE clients.
6. Close SQLite.
7. Exit within 20 seconds.

Do not allow goroutines to outlive the application context.

## 5. Download domain model

### 5.1 Job

A job is created from one MEGA public link. A folder link creates one job containing multiple files.

Job states:

```text
queued
resolving
ready
downloading
paused
waiting_quota
finalizing
completed
failed
cancelled
```

State transitions MUST be centralized in `internal/download/state.go`. API handlers do not set state directly.

### 5.2 File

File states:

```text
pending
downloading
paused
waiting_quota
verifying
moving
completed
failed
cancelled
```

Persist at minimum:

```text
id
job_id
remote_node_id
remote_path
final_relative_path
size_bytes
segment_size_bytes
segment_count
completed_segments_bitmap
bytes_committed
state
last_error_code
last_error_message
created_at
updated_at
completed_at
```

### 5.3 Segment bitmap

Do not create one SQLite row per segment.

Store a compact bitset BLOB on each file. One bit represents one fixed-size segment. With an 8 MiB segment size, even very large files require a small resume bitmap.

The scheduler may re-download uncheckpointed segments after a hard crash. It MUST never trust a segment as complete unless the corresponding file data was synced before the bit was persisted.

## 6. Link support

MVP MUST accept:

```text
https://mega.nz/file/<id>#<key>
https://mega.nz/folder/<id>#<key>
```

Also support legacy public-link syntax if it can be covered with parser tests without adding a second protocol implementation.

MVP does NOT include MegaCrypter, DLC containers, browser clipboard sniffing, or non-MEGA hosts.

### Link resolution flow

`ResolveLink(ctx, rawURL, accountID)` returns:

```go
type ResolvedJob struct {
    Kind        LinkKind
    DisplayName string
    TotalBytes  int64
    Files       []ResolvedFile
}

type ResolvedFile struct {
    NodeID       string
    RelativePath string
    Size         int64
    FileKey      []byte
    Attributes   map[string]string
}
```

Resolution MUST NOT begin downloading file payloads.

For created jobs, persist enough source material to re-resolve after restart without asking the browser to resubmit the link. Never persist the raw public URL including its decryption key in plaintext. Store non-secret source identifiers separately and encrypt the link key or equivalent source secret with the application secret.

Folder resolution MUST preserve the remote directory tree.

All path components MUST be sanitized before touching the filesystem:

- reject NUL;
- remove `/` from a component;
- reject `.` and `..` as components;
- ensure the resulting path stays under the configured root after `filepath.Clean` and `filepath.Rel` checks;
- keep Unicode names;
- resolve collisions with `name (1).ext`, `name (2).ext`, etc. when conflict policy is `rename`.

## 7. MEGA accounts

MVP MUST support zero or more MEGA accounts.

Use cases:

- anonymous public-link download;
- authenticated public-link download using a selected account;
- MEGA Pro accounts using their normal authenticated transfer allowance;
- account health/status display in Settings.

Do NOT build a remote cloud-drive browser in MVP.

Account fields:

```text
id
label
email
credential_ciphertext OR session_ciphertext
status
default_for_downloads
last_checked_at
created_at
updated_at
```

Rules:

1. Never persist plaintext passwords, TOTP secrets, or session tokens.
2. The API MUST never return secret material after creation.
3. Downloader-v1 acceptance requires email/password login for standard and Pro accounts that do not use MFA/2FA. This is sufficient to satisfy Pro-account support in the initial downloader scope.
4. The current `go-mega` library has unresolved MFA/2FA and session-login gaps. Do not invent or expose an unverified session-token flow. MFA/2FA account login is explicitly deferred from downloader-v1; show `MFA-enabled accounts are not supported in this version` during account setup when detected. Never instruct users to disable MFA/2FA.
5. A job stores its selected account ID. Do not automatically change accounts when quota is exhausted.

## 8. Secret storage

Generate a 32-byte application secret at first launch and store it at:

`/var/lib/megad/secret.key`

File mode MUST be `0600` and ownership must match the service user.

Use AES-256-GCM with a random nonce for encrypted database values.

Encrypted values include:

- MEGA passwords or session tokens;
- MEGA public-link decryption keys or equivalent resumable source secrets;
- proxy passwords;
- any future API tokens.

Never log decrypted secrets. Redact MEGA link keys from normal logs; log only a short hash of the source URL for correlation.

## 9. Proxy support

Support explicit proxy profiles:

```text
Direct
HTTP proxy
HTTPS CONNECT proxy
SOCKS5 proxy
```

Profile fields:

```text
id
name
type
host
port
username
password_ciphertext
timeout_seconds
enabled
```

Each download job stores either `proxy_id` or `NULL` for direct access.

Implementation rules:

1. Build an `http.Transport` per immutable proxy profile and reuse it.
2. Set connection pooling limits explicitly.
3. Proxy health test button performs one bounded request and shows latency/result.
4. Transport failures may be retried through the same selected proxy.
5. Do not automatically rotate proxies when MEGA returns HTTP 509, transfer-over-quota errors, or equivalent quota signals.
6. Do not maintain a harvested public proxy pool in MVP.
7. Do not fetch proxy lists from remote URLs in MVP.

## 10. Transfer engine

### 10.1 Defaults

```text
segmentSizeBytes        = 8 MiB
workersPerFile          = 4
maxActiveFiles          = 2
maxGlobalWorkers        = 8
connectTimeout          = 15 s
responseHeaderTimeout   = 30 s
readIdleTimeout         = 90 s
normalRetryLimit        = 5
checkpointInterval      = 2 s
checkpointBytes         = 256 MiB
uiProgressInterval      = 250 ms
```

Expose segment size, workers per file, active files, global workers, and bandwidth limits in Advanced Settings. Keep safe bounds:

```text
segment size:       1 MiB .. 64 MiB
workers per file:   1 .. 16
active files:       1 .. 16
global workers:     1 .. 64
```

Enforce `workersPerFile * maxActiveFiles <= maxGlobalWorkers` at runtime by the global semaphore; the settings UI may allow values that exceed the product as long as the global limit is enforced.

### 10.2 Segment scheduler

A file of size `N` is split into fixed ranges of `segmentSizeBytes`, except the final segment.

Scheduler rules:

1. Skip completed bits from the resume bitmap.
2. Prefer the lowest unfinished segment index to keep disk writes mostly sequential.
3. Each worker claims one segment atomically.
4. A failed segment returns to the pending queue unless the job enters a terminal state.
5. No segment is assigned to two workers simultaneously.
6. A file cannot exceed its configured worker count.
7. The global semaphore controls total network workers across all files.

### 10.3 HTTP range download

For each segment:

1. Acquire or refresh the MEGA payload URL through the MEGA client.
2. Issue a range request for exactly the segment byte range.
3. Validate the HTTP status and returned range.
4. Stream encrypted bytes through the MEGA AES-CTR decryptor using the correct offset/counter.
5. Write decrypted bytes directly into the single `.mega.part` file with `WriteAt`.
6. Use a reusable buffer from `sync.Pool`; default buffer size 512 KiB.
7. Mark the segment complete only in memory.
8. The checkpoint manager later syncs data and persists completion bits.

Do not buffer an entire 8 MiB segment in memory.

### 10.4 Partial file allocation

At file creation:

1. Derive the incomplete path under `<incompleteRoot>/<jobID>/<sanitized remote relative path>.mega.part`. The job-scoped directory is mandatory so two concurrent jobs with the same remote filename cannot collide.
2. Create the parent directory through the root-confined filesystem helper.
3. Open the `.mega.part` file read/write.
4. `Truncate` to the final expected size so random writes are addressable while allowing a sparse file on normal Linux filesystems.
5. Do not call an eager full-block preallocation API by default.

Disk usage target: partial data plus SQLite/log overhead only. No full-size merge copy.

### 10.5 Checkpoint ordering

A checkpoint consists of:

1. Pause collection of new completed-bit persistence; workers may continue writing.
2. Snapshot the in-memory set of newly completed segments.
3. Call `file.Sync()` for files with new completed segments.
4. In one SQLite transaction per checkpoint cycle, persist the corresponding completion bitmaps and committed byte counts.
5. Only after the transaction commits may those segments be treated as durable across restart.

Trigger a checkpoint when either condition is met:

- 2 seconds since last checkpoint; or
- 256 MiB newly completed since last checkpoint.

Always checkpoint on pause, graceful shutdown, and before final verification.

### 10.6 Integrity

Integrity verification is mandatory before atomic completion.

Implement MEGA file integrity verification in `internal/mega/integrity.go` using tested protocol-compatible crypto primitives. Prefer a tested implementation from `go-mega` where possible. If behavior is ported from GPL MegaBasterd code, keep GPL licensing and attribution.

Tests MUST include deterministic known vectors for:

- key derivation from a public link;
- AES-CTR decryption at offset 0;
- AES-CTR decryption at a non-zero aligned and non-aligned offset;
- complete-file integrity success;
- one-byte corruption failure.

A file with failed integrity MUST become `failed` and MUST NOT be renamed into the complete directory.

### 10.7 Finalization

On successful integrity verification:

1. Close the partial file.
2. Ensure final parent directory exists.
3. Resolve conflict policy.
4. Rename from incomplete to complete.
5. Persist `completed` state and final path.
6. Emit completion event.

If rename returns EXDEV, do NOT silently copy. Mark the file failed with a clear message that incomplete and complete roots must share a filesystem.

## 11. Retry and quota behavior

Centralize classification in `internal/download/retry.go`.

### Retryable transport failures

Examples: connection reset, temporary DNS error, timeout, truncated body.

Policy:

```text
attempt 1: 1 s + jitter
attempt 2: 2 s + jitter
attempt 3: 4 s + jitter
attempt 4: 8 s + jitter
attempt 5: 16 s + jitter
then fail the segment/file
```

### HTTP 429

Respect `Retry-After` when present. Otherwise use exponential backoff capped at 5 minutes.

### HTTP 5xx

Retry up to the normal retry limit.

### HTTP 401/403 on payload URL

Refresh the MEGA payload URL once and retry the segment. If it still fails, surface the error.

### HTTP 509 or MEGA over-quota error

1. Cancel/pause all workers for the affected job/account context.
2. Change job/file state to `waiting_quota`.
3. Preserve all partial data and resume bitmap.
4. If MEGA exposes a retry delay or quota-reset time, use it.
5. Otherwise retry availability checks at 60 s, 120 s, 300 s, then every 15 minutes.
6. Show the state and next retry time in the UI.
7. Do not switch account or proxy automatically.
8. Manual `Resume now` may trigger one immediate availability check but must not spin in a tight loop.

This is the primary mechanism for reliable large transfers: persistent resume, authenticated Pro use when configured, bounded concurrency, and quota-aware continuation.

## 12. Bandwidth limiting

Implement optional global and per-download speed limits in the backend.

Use a token-bucket limiter around network reads. `0` means unlimited.

Settings:

```text
globalDownloadLimitBytesPerSecond
perJobDefaultLimitBytesPerSecond
```

The UI accepts human-friendly MiB/s values and converts to bytes/s.

Do not implement speed limiting in the browser.

## 13. SQLite schema

Migration `001_initial.sql` MUST create these tables:

```text
schema_migrations
settings
users
sessions
mega_accounts
proxy_profiles
download_jobs
download_files
download_events
```

`download_events` stores only state transitions, warnings, and errors; retain the newest 500 events per job.

Enable:

```sql
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
PRAGMA synchronous=NORMAL;
```

Use `FULL` synchronous only for explicit critical migration operations if needed. File durability is handled by the checkpoint ordering described above.

## 14. HTTP API contract

All responses use JSON except SSE and static files.

Success body convention:

```json
{ "data": {} }
```

Error body convention:

```json
{
  "error": {
    "code": "string_code",
    "message": "Human readable English message",
    "details": {}
  }
}
```

Required endpoints:

```text
GET    /api/v1/health
GET    /api/v1/version

POST   /api/v1/auth/login
POST   /api/v1/auth/logout
GET    /api/v1/auth/me

POST   /api/v1/downloads/resolve
POST   /api/v1/downloads
GET    /api/v1/downloads
GET    /api/v1/downloads/:id
POST   /api/v1/downloads/:id/pause
POST   /api/v1/downloads/:id/resume
POST   /api/v1/downloads/:id/retry
POST   /api/v1/downloads/:id/cancel
DELETE /api/v1/downloads/:id

POST   /api/v1/queue/pause
POST   /api/v1/queue/resume

GET    /api/v1/settings
PUT    /api/v1/settings

GET    /api/v1/accounts
POST   /api/v1/accounts
POST   /api/v1/accounts/:id/test
PUT    /api/v1/accounts/:id
DELETE /api/v1/accounts/:id

GET    /api/v1/proxies
POST   /api/v1/proxies
POST   /api/v1/proxies/:id/test
PUT    /api/v1/proxies/:id
DELETE /api/v1/proxies/:id

GET    /api/v1/events
```

### Resolve request

```json
{
  "url": "https://mega.nz/file/...#...",
  "accountId": null
}
```

Resolve response returns display name, total size, and a file tree. It MUST NOT return decrypted file keys.

### Create download request

```json
{
  "url": "https://mega.nz/file/...#...",
  "accountId": null,
  "proxyId": null,
  "destinationSubdirectory": "",
  "startImmediately": true
}
```

The server re-resolves the link. Do not trust metadata sent by the browser.

### Cancel request

```json
{
  "deletePartialFiles": false
}
```

### Delete semantics

Deleting a database job MUST require explicit `deleteFiles=true` to remove already completed payloads. Default is metadata-only deletion for completed jobs and partial-file deletion prompt for unfinished jobs.

## 15. SSE contract

Endpoint: `GET /api/v1/events`

Event names:

```text
job.updated
file.updated
speed.updated
queue.updated
account.updated
settings.updated
```

Rules:

1. Coalesce progress/speed events so the browser sees at most 4 updates per second per active job.
2. Do not write every SSE event to SQLite.
3. On SSE disconnect/reconnect, the frontend invalidates affected TanStack Query caches and refetches current state.
4. Keep-alive comment every 15 seconds.
5. SSE endpoint requires normal authenticated session.
6. Each SSE client has a small bounded outbound queue. Slow clients must not block transfer workers or the event bus; coalesce replaceable progress events and disconnect a client that remains unable to drain.
7. Do not implement durable SSE replay for MVP. After reconnect, invalidate/refetch server state and resume live telemetry from the new snapshot.

## 16. Web authentication

MVP has one local admin user.

First-run behavior:

1. If no user exists, `/setup` is the only application route available after health/static assets.
2. User sets username and password.
3. Hash password with Argon2id using a unique salt.
4. Create an opaque random session token with at least 256 bits of entropy. Store only a fast cryptographic digest (SHA-256 or keyed HMAC-SHA-256) in SQLite; Argon2id is for human passwords, not high-entropy session tokens.
5. Send the raw token only in an `HttpOnly`, `SameSite=Lax` cookie.
6. After setup, disable the setup endpoint.

Mutation protection:

- require same-origin `Origin`/`Host` checks on unsafe methods;
- never accept auth tokens in URL query strings;
- use secure cookies automatically when the request is HTTPS or when configured behind a trusted reverse proxy.

Do not implement OAuth/OIDC in MVP.

## 17. Frontend implementation

### 17.1 Toolchain bootstrap

Use Vite+ and Node 24 LTS:

```bash
# one-time bootstrap / intentional dependency update
vp env pin 24
vp install

# CI and normal reproducible verification
vp install --frozen-lockfile
```

Use the Vite React template, TypeScript 7, and the TanStack Router Vite plugin. Pin the published `vite-plus` release and the exact TypeScript package version in the committed lockfile. Never use a Vite+ preview/commit build as the repository baseline.

Use Tailwind through its Vite plugin and initialize shadcn/ui for the existing Vite app.

### 17.2 Required frontend dependencies

```text
react
react-dom
@tanstack/react-router
@tanstack/router-plugin
@tanstack/react-query
@tanstack/react-table
@tanstack/charts
@tanstack/charts-scales
@tanstack/react-charts
tailwindcss
@tailwindcss/vite
shadcn
lucide-react
i18next
react-i18next
zod
```

Do not add a debounce library for the Add Download flow. Do not add Redux, Zustand, MobX, Axios, or a second router. Use `fetch` plus TanStack Query.

### 17.3 Routes

Create:

```text
/setup
/
/downloads
/downloads/$downloadId
/settings
/settings/general
/settings/downloads
/settings/accounts
/settings/proxies
/settings/appearance
```

### 17.4 Dashboard `/`

Display:

- total current download speed;
- active jobs;
- queued jobs;
- waiting-for-quota jobs;
- bytes downloaded this session;
- disk free space for the complete root;
- primary `Add download` button;
- compact list of active jobs.

Do not create an analytics-heavy dashboard.

### 17.5 Add Download dialog

The primary workflow must be possible without leaving the dashboard.

Fields:

```text
MEGA URL textarea (one URL in MVP; structure should allow multiple later)
Account select: Anonymous + configured accounts
Proxy select: Direct + configured profiles
Destination subdirectory
Start immediately toggle
```

Flow:

1. User pastes URL.
2. User clicks `Resolve`. Do not auto-resolve while typing and do not add a redundant debounce.
3. Show metadata: name, kind, total size, file count.
4. For folder links, show a read-only collapsible file tree.
5. User clicks `Add download`.
6. Dialog closes and job appears immediately in queue.

Never expose link decryption keys after submission.

### 17.6 Downloads page

Use TanStack Table.

Columns:

```text
Name
Status
Progress
Size
Speed
ETA
Files
Account
Proxy
Added
Actions
```

Features:

- sortable name/status/size/added columns;
- filters for state and text search;
- row selection only if batch actions are implemented;
- pause/resume/retry/cancel actions;
- progress bar in row;
- responsive card layout below tablet width.

No pagination is needed until more than 500 persisted jobs; use virtualized rendering only if measurement proves it necessary.

### 17.7 Download detail

Display:

- title and state badge;
- total progress and speed;
- ETA;
- source type and destination path;
- selected account/proxy labels;
- file table for folder jobs;
- last 200 state/warning/error events;
- throughput chart for the last 30 minutes.

Chart data is client-side rolling telemetry from SSE and does not need permanent storage.

### 17.8 Settings

General:

```text
complete root
incomplete root
start downloads automatically
conflict policy
UI theme
language (English only, disabled/select with one option)
```

Downloads:

```text
segment size
workers per file
max active files
max global workers
global speed limit
checkpoint interval (advanced)
retry limits (advanced)
```

Accounts:

- list accounts;
- add/edit/remove;
- test account;
- mark default;
- never show stored password/session.

Proxies:

- list profiles;
- add/edit/remove;
- test proxy;
- mark default;
- explicit note that proxy routing does not override MEGA quota policy.

Appearance:

- light/dark/system;
- locale placeholder for future languages.

## 18. i18n rules

All user-visible strings live in `web/src/i18n/locales/en/common.json`.

Keys use stable semantic names, for example:

```text
nav.downloads
nav.settings
download.add
download.status.waitingQuota
settings.accounts.title
errors.linkInvalid
```

Do not build English sentences by concatenating translated fragments.

Backend error objects use stable machine codes. The frontend maps known codes to translated user-facing strings and falls back to the backend message for unknown errors.

## 19. Settings model

Backend settings are the source of truth. LocalStorage may contain only UI preferences such as table column widths.

Required backend settings with defaults:

```json
{
  "paths": {
    "incompleteRoot": "/mnt/media/downloads/mega/incomplete",
    "completeRoot": "/mnt/media/downloads/mega/complete"
  },
  "downloads": {
    "autoStart": true,
    "segmentSizeBytes": 8388608,
    "workersPerFile": 4,
    "maxActiveFiles": 2,
    "maxGlobalWorkers": 8,
    "globalSpeedLimitBytesPerSecond": 0,
    "conflictPolicy": "rename",
    "checkpointIntervalMs": 2000,
    "checkpointBytes": 268435456,
    "normalRetryLimit": 5
  },
  "network": {
    "connectTimeoutSeconds": 15,
    "responseHeaderTimeoutSeconds": 30,
    "readIdleTimeoutSeconds": 90
  },
  "ui": {
    "theme": "system",
    "locale": "en"
  }
}
```

Validate every PUT on the backend. Reject invalid settings atomically; never partially apply a settings payload.

Settings that affect only new workers may be applied immediately to future segment claims. Path changes apply only to newly created jobs; existing jobs keep their persisted roots.

## 20. API and filesystem security

Mandatory rules:

1. Never let the browser supply an absolute destination path for a job.
2. Browser supplies only an optional relative subdirectory under the configured complete root.
3. Use `filepath.Rel` containment checks after joining paths.
4. Symlink escape: before writing, walk/create controlled parent directories and reject any existing symlink in the path between root and target.
5. Bind address default: `127.0.0.1:8080`. LXC/LAN deployments opt in explicitly to `0.0.0.0:8080` (or a specific LAN address) after the admin bootstrap is understood. README must document reverse-proxy/VPN/LAN exposure. Never trust `X-Forwarded-*` headers unless the peer is in an explicitly configured trusted-proxy list.
6. Apply request body size limits. Link-add request maximum 256 KiB.
7. HTTP server timeouts must be explicit.
8. Never expose a generic filesystem browser endpoint.

## 21. Performance design constraints

The implementation is considered structurally wrong if it violates any of these:

- no Java runtime;
- no Node.js runtime in production;
- no chunk temp files;
- no merge copy;
- no polling loop from the browser faster than 5 seconds; use SSE instead;
- no per-progress-update SQLite transaction;
- no unbounded goroutine creation;
- no unbounded in-memory event history;
- no download buffer larger than 1 MiB per worker by default;
- no React global state library for server data.

Target resource envelope with default settings on Linux amd64:

```text
Idle RSS:                    <= 80 MiB
8 active network workers:    <= 180 MiB RSS
UI live update rate:         <= 4 Hz per job
SQLite progress commits:     <= 2 checkpoint transactions/sec normally
Temporary disk overhead:     < 1% beyond downloaded payload data
```

These are acceptance targets, not reasons to weaken integrity or resume correctness.

### 21.1 Resource-profile acceptance

Benchmark both supported LXC profiles before release:

```text
small:  2 vCPU / 4 GiB RAM
large:  4 vCPU / 8 GiB RAM
```

Rules:

- the existing RSS numbers are optimization targets, not permission to weaken correctness;
- the downloader-only process MUST remain comfortably below 512 MiB RSS on the small profile under the default 8-worker stress fixture;
- if 8 workers produce less than a 10% throughput gain over a lower worker count while materially increasing CPU saturation, choose the lower default and document the benchmark;
- all worker pools, SSE client queues, event histories, retry timers, and future caches have explicit upper bounds;
- Phase H records RSS, CPU, open FDs, SQLite write rate, and throughput for 1/4/8 workers on the small profile and 8/12 workers on the large profile;
- tune from measurements, not from assumed worker counts. Advanced settings may expose higher values, but safe defaults must be benchmark-derived.

## 22. Tests - mandatory before UI polish

### 22.1 Backend unit tests

Create tests for:

- MEGA URL parsing;
- folder/file link classification;
- path sanitization and traversal rejection;
- segment planning for 0, 1 byte, exact segment, segment+1, and multi-TiB sizes;
- bitset resume serialization;
- scheduler race safety;
- retry classification;
- `Retry-After` parsing;
- quota state transitions;
- AES secret store encrypt/decrypt;
- corrupt secret failure;
- proxy URL/auth parsing;
- settings validation;
- conflict-path naming;
- SSE coalescing.

Run with `go test -race ./...` on amd64 Linux in CI.

### 22.2 Fake MEGA integration server

Implement `tests/integration/fake_mega_server.go`.

It must support:

- deterministic metadata response;
- range requests;
- deterministic encrypted payload generation;
- delayed segments;
- connection reset mid-segment;
- malformed Content-Range;
- HTTP 429 with Retry-After;
- HTTP 500/503;
- HTTP 509;
- payload URL expiry followed by refresh success;
- one-byte corruption injection.

This fixture is required so CI does not depend on MEGA availability or a real account.

Also provide an opt-in `make test-live` compatibility smoke test against a maintainer-controlled public MEGA fixture. It is not a normal PR gate, but it SHOULD run on a scheduled workflow and before releases so upstream MEGA protocol drift is detected independently of the fake server. The fixture must contain no private credentials or copyrighted test payload beyond a tiny project-owned blob.

### 22.3 Core integration scenarios

All must pass:

1. Single 100 MiB file downloads and integrity matches expected hash.
2. Folder job with nested directories preserves structure.
3. Four workers produce identical output to one worker.
4. Process killed around 35% resumes without restarting from zero.
5. Crash after file write but before checkpoint redownloads only uncommitted segments and still produces correct output.
6. 503 retries and recovers.
7. 429 waits according to Retry-After.
8. 509 enters `waiting_quota`; no selected proxy/account changes; later server recovery resumes the same job.
9. One corrupt segment fails integrity and does not appear in complete root.
10. Cancel with `deletePartialFiles=false` preserves partial data.
11. Cancel with `deletePartialFiles=true` removes partial data.
12. EXDEV finalization is surfaced as a configuration error, not silently copied.
13. HTTP proxy fixture proves all transfer requests use the chosen profile.
14. SOCKS5 proxy fixture proves the same.

### 22.4 Frontend tests

Use Vitest for component/unit tests and Playwright for browser E2E.

Required E2E flows:

1. First-run admin setup.
2. Login/logout.
3. Add valid single-file link through fake backend.
4. Invalid link error.
5. Add folder link and view tree.
6. Pause/resume job.
7. 509 state shows waiting message and next retry.
8. Add/test/remove account.
9. Add/test/remove proxy.
10. Change settings and reload; values persist.
11. SSE disconnect/reconnect does not lose current status.
12. Dark/light/system theme switch.

## 23. Observability

Use structured `slog` fields even with text output:

```text
component
download_id
file_id
segment_index
account_id
proxy_id
http_status
retry_attempt
error_code
```

Do not log:

- MEGA passwords;
- session tokens;
- proxy passwords;
- complete public-link decryption keys.

Provide:

```text
GET /api/v1/health
```

Response includes only:

```json
{
  "data": {
    "status": "ok",
    "version": "...",
    "database": "ok",
    "downloadManager": "ok"
  }
}
```

Prometheus metrics are deferred; do not add a metrics dependency in MVP.

## 24. Build and release

### Frontend

Pin Node 24 through Vite+ and commit the lockfile.

Required commands:

```bash
cd web
vp install --frozen-lockfile
vp check
vp test
vp build
```

### Backend

Required commands:

```bash
go fmt ./...
go vet ./...
go mod verify
govulncheck ./...
go test -race ./...
go build -trimpath -o dist/megad ./cmd/megad
```

### Root Makefile

Implement at minimum:

```text
make dev
make check
make test
make build
make clean
```

`make build` must:

1. build frontend;
2. replace `internal/webui/dist` with `web/dist`;
3. build Go binary;
4. output `dist/megad`.

Production must not require Node or Java.

## 25. systemd/LXC packaging

Create system user `megad`.

Expected runtime permissions:

```text
/var/lib/megad                       rw
/mnt/media/downloads/mega/incomplete rw
/mnt/media/downloads/mega/complete   rw
```

`packaging/megad.service` must include:

```text
User=megad
Group=media
StateDirectory=megad
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
CapabilityBoundingSet=
AmbientCapabilities=
ReadWritePaths=/var/lib/megad /mnt/media/downloads/mega
```

Do not enable `PrivateNetwork` because the downloader needs outbound network access.

README must include a Proxmox/LXC note: bind-mount `/mnt/media` into the LXC at the same path and ensure the `megad` user/group has write permission.

## 26. UI visual specification

The UI should look like a modern infrastructure/download application, not a desktop Java port.

Rules:

- desktop-first responsive layout;
- left sidebar on desktop, compact top navigation on mobile;
- neutral shadcn palette with system dark mode;
- use cards sparingly;
- queue/table is the dominant information surface;
- progress bars are thin and data-dense;
- status uses text + icon + color, never color alone;
- dialogs only for creation/destructive confirmations;
- settings use normal pages with sections, not nested modal dialogs;
- skeletons only for initial fetches;
- toasts only for action confirmation/errors, not progress;
- keyboard accessible controls and visible focus rings;
- no gradients, glassmorphism, animated backgrounds, or oversized marketing typography.

## 27. Implementation sequence for agents

Agents MUST implement in this order. A phase is complete only when its gate passes. Do not continue by weakening a failing gate. Upload and streaming plans remain out of scope.

### Phase A - repository/bootstrap

Tasks:

- create repository/package layout;
- add GPL-3.0 license and NOTICE;
- initialize Go module;
- initialize Vite+ React + TypeScript 7 app;
- configure TanStack Router, Tailwind, shadcn, Query, i18n;
- pin frontend dependencies and lockfile;
- add Makefile and CI;
- add empty migrations framework;
- build the embedded blank UI.

Gate:

```text
make build succeeds
vp check / vp test / vp build succeed
go test ./... succeeds
blank shell renders from embedded Go binary
```

### Phase B - MEGA protocol/crypto compatibility spike

This phase exists to kill the largest dependency risk early. Do not build persistence-heavy UI around unverified protocol assumptions.

Tasks:

- modern public file-link parser;
- modern public folder-link parser and recursive metadata resolution;
- public payload URL acquisition needed by range downloads;
- key derivation and AES-CTR decryption at offset 0 and arbitrary offsets;
- integrity/MAC verification vectors;
- verify exactly which `go-mega` primitives are reusable and wrap them behind `internal/mega`;
- fake-server fixtures plus one opt-in live public fixture smoke test.

Gate:

```text
public file fixture resolves without payload download
public folder fixture resolves recursively
known crypto/integrity vectors pass
a ranged encrypted fixture decrypts correctly at aligned and unaligned offsets
project does not rely on an unimplemented public-link API in go-mega
```

If this gate cannot pass cleanly, stop and record the protocol blocker in `BLOCKERS.md`. Do not proceed to application UI work.

### Phase C - store/settings/auth + resolve surface

Tasks:

- SQLite/WAL/migrations;
- secret key and AES-GCM store, including encrypted public-link source secrets;
- first-run admin setup/session auth;
- typed settings with atomic validation;
- root-confined filesystem helper;
- settings API/UI;
- resolve API and Add Download resolve UI;
- path sanitization and collision rules.

Gate:

```text
restart preserves settings and login state
secrets and public-link keys are not plaintext in SQLite
session tokens are stored only as cryptographic digests
resolve never downloads payload data
path/symlink containment tests pass
```

### Phase D - transfer core, single worker

Tasks:

- job/file models;
- job-scoped partial-file path;
- segment planner;
- one range worker;
- ranged download/decrypt/WriteAt;
- checkpoint manager with sync-before-bitmap ordering;
- resume after restart;
- integrity verification;
- atomic finalization.

Gate:

```text
100 MiB fixture completes
kill/restart resumes
crash around checkpoint boundary is correct
corruption fails
same-name concurrent jobs cannot collide
no chunk temp files or merge copy are created
```

### Phase E - parallel scheduler

Tasks:

- per-file worker concurrency;
- global semaphore;
- queue priorities/FIFO;
- speed meter;
- bandwidth limiter;
- pause/resume/cancel;
- bounded goroutine ownership;
- race tests and resource benchmarks.

Gate:

```text
1-worker and 4-worker output hashes match
race detector passes
small-profile default worker benchmark stays inside resource envelope
no unbounded goroutine/FD growth
```

### Phase F - retry/quota/proxy/accounts

Tasks:

- retry classifier/backoff;
- 429/5xx/expired URL handling;
- 509/waiting-quota flow;
- proxy profiles HTTP/SOCKS5 with transport lifecycle cleanup;
- account login/selection per job;
- account/proxy tests and UI.

Gate:

```text
all failure-injection integration tests pass
509 never changes proxy/account automatically
resume continues the same partial file
editing/deleting a proxy profile does not leak stale transports
```

### Phase G - live UI

Tasks:

- event bus;
- bounded/coalescing SSE endpoint;
- downloads table;
- detail page;
- isolated throughput chart adapter;
- dashboard;
- all actions wired to API;
- accessibility pass.

Gate:

```text
all required Playwright flows pass
no frontend polling faster than 5 seconds
progress is driven by SSE
slow/disconnected SSE clients cannot block transfer workers
chart failure cannot break the download detail route
```

### Phase H - packaging/performance/security/release

Tasks:

- systemd unit and hardening;
- single-binary release;
- LXC README for both resource profiles;
- graceful shutdown;
- small/large profile resource matrix;
- log redaction tests;
- path/symlink security tests;
- `govulncheck`, dependency and license audit;
- opt-in live MEGA compatibility smoke;
- version/commit build metadata exposed by `/api/v1/version`.

Gate:

```text
fresh Debian LXC can run the binary as a service
Node and Java are absent at runtime
small 2-vCPU/4-GiB profile passes the measured resource envelope
all unit/integration/Playwright/race/security gates pass
live MEGA smoke passes before release
```

## 28. Definition of done

The MVP is DONE only when every item is true:

- [ ] Public MEGA file links download successfully.
- [ ] Public MEGA folder links recursively download and preserve structure.
- [ ] Anonymous download works.
- [ ] A configured MEGA account can be selected for a job.
- [ ] MEGA Pro account usage follows the normal authenticated account path.
- [ ] HTTP/HTTPS and SOCKS5 proxy profiles can be configured and selected.
- [ ] Parallel ranged downloads work.
- [ ] Partial downloads survive process restart.
- [ ] HTTP 509 / over-quota becomes a persistent waiting state and later resumes.
- [ ] No automatic proxy/account rotation is performed to bypass quotas.
- [ ] File integrity is verified before completion.
- [ ] Partial data uses one `.mega.part` file per target file.
- [ ] Completion uses same-filesystem atomic rename.
- [ ] Default final path is `/mnt/media/downloads/mega/complete`.
- [ ] Queue, pause, resume, retry, cancel, delete are fully manageable from browser UI.
- [ ] All application settings required above are manageable from browser UI.
- [ ] Account, proxy, and public-link source secrets are encrypted at rest.
- [ ] Session tokens are stored only as fast cryptographic digests, never plaintext.
- [ ] Same-name jobs cannot collide in the incomplete directory.
- [ ] UI is English and all strings go through i18n resources.
- [ ] React UI is CSR and uses TanStack Router, not TanStack Start.
- [ ] Production is a single Go binary with embedded static assets.
- [ ] Production requires neither Node.js nor Java.
- [ ] `go test -race ./...` passes.
- [ ] Backend integration suite passes.
- [ ] Playwright suite passes.
- [ ] Resource envelope is measured on both 2-vCPU/4-GiB and 4-vCPU/8-GiB profiles and documented in README.
- [ ] A release-time live public-MEGA compatibility smoke test passes.
- [ ] GPL/third-party notices are present.

## 29. Explicitly deferred

Do not implement these in the main plan:

- uploads to MEGA;
- streaming/proxy streaming server;
- *arr automatic imports;
- clipboard watcher;
- browser extension;
- MegaCrypter;
- remote proxy-list harvesting;
- automatic IP/proxy/account rotation on quota exhaustion;
- multi-user/RBAC;
- OIDC;
- cloud drive browser;
- mobile native app;
- Prometheus;
- plugin system.

Upload and streaming have separate plans so MVP agents do not accidentally expand scope.
