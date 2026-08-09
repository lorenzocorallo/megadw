# MEGA Downloader

MEGA Downloader is a single-process Go application with a client-rendered React web interface. The repository is being implemented phase by phase from [PLAN.md](PLAN.md); the current tree contains the Phase D persistence-backed single-worker transfer core on top of the Phase C authentication, settings, filesystem confinement, and Phase B public-link protocol surface.

## Toolchain

- Go module language version: Go 1.26.0, with toolchain Go 1.26.5.
- Node.js: 24.19.0, pinned in `.node-version` and managed by Vite+.
- Vite+: 0.2.6, pinned in `web/package.json` and `web/package-lock.json`.
- TypeScript: 7.0.2, exact-pinned in `web/package.json` and the lockfile.

Install the published Vite+ CLI, select the pinned Node version, and install the frozen frontend dependencies:

```bash
curl -fsSL https://vite.plus | VP_VERSION=0.2.6 bash
vp env pin 24
cd web
vp install --frozen-lockfile
```

Vite+ is a build-time dependency only. If a blocking Vite+ regression requires a rollback, keep the React, router, Query, and UI contracts unchanged and replace the Vite+ commands with plain Vite, Vitest, Oxlint, and Oxfmt commands; do not introduce a second production runtime.

## Verification

```bash
make build
make check
make test
```

The Phase B live compatibility smoke is opt-in:

```bash
MEGADW_LIVE_MEGA_URL='https://mega.nz/file/...' make test-live
```

The production build places the Go binary at `dist/megad`. It serves the frontend from embedded assets and listens on `127.0.0.1:8080` by default:

```bash
./dist/megad
```

The production process does not require Node.js or Java.

On first launch, open the application at `/setup` and create the single local administrator account. The account session is held in an HttpOnly SameSite cookie; passwords, public-link keys, payload locations, and other transfer secrets are encrypted or hashed at rest. The browser can resolve a public MEGA link and add its metadata to the queue without fetching payload bytes.

Downloads use one job-scoped sparse `.mega.part` file per remote file. Completed files are integrity-checked and atomically renamed into the complete root; a restart rehydrates the persisted segment bitmap and resumes unfinished ranges.

## License

The project is licensed under GPL-3.0-only. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
