# Future Plan - MEGA Streaming

Status: deferred; do not execute during downloader MVP
Dependency: main `PLAN.md` must be released and stable first
Resource target: streaming must remain bounded on the same 2 vCPU / 4 GiB minimum resource profile and share global network/resource budgets with downloads

## Goal

Expose a selected MEGA file as a local HTTP streaming endpoint with correct byte-range behavior, bounded caching, and minimal disk usage.

This feature is for user-controlled playback/access to files the user is authorized to access. It is not a quota-bypass system.

## Architecture

Keep streaming inside the Go backend. Do not introduce a Node server or TanStack Start.

Create:

```text
internal/stream/
  service.go
  session.go
  range.go
  cache.go
  prefetch.go
```

Use the existing:

- MEGA link/account resolver;
- proxy profiles;
- secret store;
- HTTP transports;
- bandwidth limiter;
- event bus;
- authentication middleware.

## Streaming API

```text
POST   /api/v1/streams
GET    /api/v1/streams/:id
DELETE /api/v1/streams/:id
GET    /stream/:token
```

`POST /api/v1/streams` accepts a MEGA link, optional account, optional proxy, and cache policy. It returns a short-lived stream URL.

The playback URL uses an opaque random token with at least 256 bits of entropy. Store only its cryptographic digest server-side, scope it to one stream session, give it a short expiry, and revoke it when the session is deleted. Never place the original MEGA URL/key in the playback URL.

## HTTP behavior

The stream endpoint MUST support:

- `HEAD`;
- `GET`;
- single and bounded multiple byte `Range` requests;
- `206 Partial Content`;
- `Content-Range`;
- `Accept-Ranges: bytes`;
- correct content length;
- a useful `Content-Type` derived from trusted metadata/extension with a safe fallback;
- `Content-Disposition` with a sanitized filename;
- disconnect cancellation.

For multiple ranges, implement standards-compliant `multipart/byteranges` with a small maximum range count (default 4) and normalized/merged ranges. Return `416` only for unsatisfiable or deliberately rejected excessive/abusive range sets; do not return `416` merely because a valid request contains more than one range.

## Fetch/decrypt model

- Translate requested plaintext byte ranges into MEGA payload ranges.
- Decrypt with AES-CTR at arbitrary offsets.
- Stream to client with a bounded buffer.
- Do not download the entire file first.
- Use small forward prefetch only when requests are sequential.

## Cache

Default cache is memory-only with a hard **global** cap across all sessions. The cap is not multiplied per stream.

Suggested defaults:

```text
global memory cache:        64 MiB
per-session prefetch:        4 MiB
default active sessions:     2
configurable session max:    4
idle session timeout:       10 min
max ranges/request:          4
```

Cache entries contain decrypted plaintext bytes. Keep them process-memory-only by default, account for them in the global resource budget, wipe references promptly on eviction/session teardown, and never log cache contents.

Optional disk cache may be added later under `/var/cache/megadw/stream`, but it must use an LRU size cap and must not silently become a permanent second copy of streamed files.

## Quota behavior

If MEGA returns quota exhaustion while streaming:

- stop fetching new ranges;
- if detected before response headers, return `503 Service Unavailable` and `Retry-After` when MEGA provides a usable delay;
- if detected after a `206` body has started, cancel the upstream and terminate the downstream response; do not pretend a new HTTP status can be sent mid-body;
- show quota state in the UI;
- preserve no false promise of uninterrupted playback;
- do not rotate proxies/accounts automatically.

## UI

Add a `Stream` action on resolved/download-detail pages only after this plan is implemented.

Stream detail should show:

- file name/size;
- selected account/proxy;
- active clients;
- current bitrate;
- cache use;
- stop-session button;
- copy stream URL button.

Do not build a media player in the first streaming release. The feature produces an HTTP URL suitable for VLC/mpv/browser-compatible media.

## Tests

- range 0-N correctness;
- arbitrary seek to middle of file;
- unaligned AES-CTR offset correctness;
- repeated nearby ranges hit cache;
- distant seek does not grow cache without bound;
- client disconnect cancels upstream request;
- valid bounded multi-range requests return correct `multipart/byteranges`;
- excessive/unsatisfiable range sets are rejected without excessive CPU/memory work;
- concurrent clients remain within limits;
- quota failure is surfaced without identity rotation;
- stream token expires;
- stream token does not reveal MEGA key;
- RSS stays within the global cache + worker overhead;
- 2 concurrent sessions on the 2-vCPU/4-GiB profile and 4 on the 4-vCPU/8-GiB profile pass resource tests;
- stream tokens are stored only as digests and are revoked on session deletion.

## Definition of done

- [ ] VLC/mpv can seek through a large MEGA file using the local stream URL.
- [ ] Range responses are standards-compliant.
- [ ] No full-file download is required.
- [ ] Memory/disk cache is bounded.
- [ ] Authentication and expiring stream tokens are enforced.
- [ ] Account/proxy selection works.
- [ ] Quota errors fail safely and visibly.
