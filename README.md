# MEGA Downloader

MEGA Downloader is a single-process Go application with a client-rendered React web interface. The repository is being implemented phase by phase from [PLAN.md](PLAN.md); the current tree contains the Phase B public-link protocol/crypto spike and an embedded blank shell.

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

## License

The project is licensed under GPL-3.0-only. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
