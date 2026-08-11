# Development

## Toolchain

The repository pins:

- Go 1.26.5
- Node.js 24.19.0
- Vite+ 0.2.6
- TypeScript 7.0.2

Install Vite+ and the pinned Node.js version, then install frontend packages:

```bash
vp env pin 24
cd web
vp install --frozen-lockfile
cd ..
```

## Build and run

```bash
make build
./dist/megadw -state-dir "$PWD/.megadw"
```

`make build` builds the React application, copies it into the Go embed tree,
and creates `dist/megadw`. Release builds include version, commit, and UTC
build-time metadata exposed by `GET /api/v1/version`.

The default listener is `127.0.0.1:8080`. Open `/setup` to create the local
administrator. A local run still needs incomplete and complete transfer roots
configured in **Settings**.

## Checks and tests

Common targets are:

```bash
make check
make test
make build
```

The complete release checks are:

```bash
go fmt ./...
go vet ./...
go mod verify
govulncheck ./...
go test ./... -count=1
go test -race ./... -count=1
cd web && vp install --frozen-lockfile && vp check && vp test && vp build
cd ..
make audit
make graceful-shutdown
make resource-benchmark
make production-smoke
make docker-smoke
```

Tests use fake MEGA and proxy fixtures for deterministic runs. The live MEGA
compatibility smoke is opt-in and must use a maintainer-owned, project-safe
public fixture:

```bash
MEGADW_LIVE_MEGA_URL='https://mega.nz/file/...' make test-live
```

## Browser tests

Install Chromium once, then run Playwright:

```bash
cd web
vp exec playwright install --with-deps chromium
vp exec playwright test
```

`make production-smoke` builds and runs the embedded binary, performs setup and
login, exercises the API and event stream, reloads application routes, and
checks that the production process does not spawn Node.js or Java.

## Project specifications

[PLAN.md](../PLAN.md) defines the downloader MVP and its release gates. Upload
and streaming are deferred to
[PLAN-UPLOAD.md](../.docs/plans/PLAN-UPLOAD.md) and
[PLAN-STREAMING.md](../.docs/plans/PLAN-STREAMING.md); they are not part of the
current implementation scope.
