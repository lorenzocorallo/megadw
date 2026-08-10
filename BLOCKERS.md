# Release blockers

## Filesystem confinement (resolved 2026-08-10)

### Historical finding

The previous `internal/fsroot` layer checked absolute pathnames with `Lstat`
and then returned them to callers that later used `os.OpenFile`, `os.Rename`,
`os.Remove`, `os.RemoveAll`, or pathname-based verification. A symlink could
replace a checked parent between those operations and redirect access outside
the configured root.

### Resolution evidence

`internal/fsroot` now securely opens each configured root and holds its
descriptor. Nested directory creation, file open/resume, metadata lookup,
verification, removal/recursive cleanup, directory sync, conflict handling,
and cross-root finalization use descriptor-relative Linux operations with
`O_NOFOLLOW`; finalization uses anchored `renameat`/`renameat2` and preserves
`EXDEV`. Callers receive only relative names or already-open descriptors.

Deterministic interposition tests cover partial creation, resume, deletion,
recursive cleanup, final rename across unrelated roots, intermediate symlink
rejection, atomic conflict behavior, and `EXDEV`. Phase-D recovery and
same-name job tests continue to pass, including the race detector.

Status: resolved; no known filesystem-confinement release blocker remains.

## Account login leaks an unowned MEGA event-polling client

### Failing assumption

The pinned `github.com/t3rm1n4l/go-mega` account `Login` operation is not a
bounded authentication/session primitive. A successful login continues into
`postAuthInit`, downloads the account filesystem, and starts a background
event-polling goroutine. The library exposes no close, logout, or polling-stop
method. `internal/mega.Client.LoginAccount` retains only the returned session
ID and discards the `*mega.Mega` value, so every account create, test, or
password re-login permanently loses ownership of that goroutine and its HTTP
transport.

The same code path accepts a stored session ID without validating it. When a
stored session expires or is revoked, payload resolution keeps returning the
session error and never performs the available password re-login fallback.

### Minimal reproduction

No real credentials are required to demonstrate the lifecycle defect:

```sh
rg -n 'func \(m \*Mega\) (Login|postAuthInit|pollEvents)|go m\.pollEvents' \
  "$(go env GOPATH)/pkg/mod/github.com/t3rm1n4l/go-mega@v0.0.0-20251120131202-6845944c051c/mega.go"
rg -n 'LoginAccount|goMega.New|GetSessionID' internal/mega/client.go
rg -n 'func \(m \*Mega\) (Close|Logout|Stop)' \
  "$(go env GOPATH)/pkg/mod/github.com/t3rm1n4l/go-mega@v0.0.0-20251120131202-6845944c051c"
```

The call chain in the pinned source is `Login` -> `postAuthInit` ->
`getFileSystem` -> `go m.pollEvents()`. `pollEvents` is an unbounded loop, and
the final search returns no lifecycle method.

### Evidence

- Project entry point: `internal/mega/client.go`, `LoginAccount`.
- Pinned upstream: `mega.go`, `Login`, `postAuthInit`, `getFileSystem`, and
  `pollEvents`.
- Project code stores only `GetSessionID()` and does not retain the client.
- Static dependency tracing confirms that the background loop is reachable
  from account creation, account testing, and expired-session re-login.

### Attempted fixes

- Disabling the upstream logger only suppresses output; it does not stop the
  filesystem fetch or polling goroutine.
- Reading `GetSessionID()` after `Login` is too late because `Login` completes
  `postAuthInit` first.
- `LoginWithKeys` uses the same post-login initialization.
- Retaining the client would permit transport reuse but would not provide a
  supported or reliable way to stop its event poll during deletion or process
  shutdown.

### Smallest required replacement

Replace this use of upstream `Login` with a project-owned, context-aware MEGA
account authentication/session-acquisition operation that stops after session
creation and never loads or polls the account filesystem. Validate stored
sessions, fall back once to the encrypted password on an authentication-only
failure, persist the replacement session, and add deterministic fake-MEGA
tests proving bounded goroutine/transport lifecycle and graceful shutdown.
Do not enable account or proxy rotation as part of this correction.
