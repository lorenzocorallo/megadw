# MEGA Downloader (`megad`)

MEGA Downloader is a single-process Go application with a client-rendered
React web interface for resumable public MEGA file and folder downloads. The
production artifact is one Go binary with the frontend embedded in it. Node.js,
Vite+, test runners, and Java are build/test tools only; none is required at
runtime.

The downloader preserves one job-scoped sparse `.mega.part` file per remote
file, checkpoints file data before its resume bitmap, verifies MEGA integrity,
and atomically renames completed files onto the same filesystem. HTTP 509 and
other quota responses pause the selected job and never rotate accounts or
proxies.

> [!WARNING]
> This project is experimental and has been developed with substantial AI
> assistance. It has not received a complete independent security or
> reliability audit. Expect bugs and breaking changes, keep backups, grant the
> service only the filesystem access it needs, and do not expose it directly
> to the public internet.

## Why `megad`?

- One self-contained production process serves both the API and web UI; Node.js
  and Java remain build-time tools only.
- Restart-safe transfers checkpoint file data before their compact SQLite
  resume bitmaps.
- Parallel ranges write directly into one sparse partial file, avoiding chunk
  files and a second full-size merge copy.
- MEGA integrity verification completes before the same-filesystem atomic move
  into the final root.
- Worker counts, buffers, retries, events, file descriptors, and bandwidth are
  explicitly bounded for small servers.
- Quota handling pauses and resumes the selected account/proxy context; it does
  not rotate identities to evade transfer limits.

The downloader supports public file and folder links, optional standard MEGA
accounts without MFA/2FA, and explicit HTTP, HTTPS CONNECT, and SOCKS5 proxy
profiles. Account authentication is project-owned and bounded: it acquires or
validates a session without loading the private cloud tree or starting an
event-polling client. MFA/2FA account login, uploads, streaming, cloud browsing,
and automatic quota-evasion features are outside the current MVP.

Implementation and release gates live in [PLAN.md](PLAN.md). Upload and
streaming remain deferred in
[PLAN-UPLOAD.md](.docs/plans/PLAN-UPLOAD.md) and
[PLAN-STREAMING.md](.docs/plans/PLAN-STREAMING.md).

## Build and verify

The pinned toolchain is Go 1.26.5, Node.js 24.19.0, Vite+ 0.2.6, and
TypeScript 7.0.2. Install Vite+ and the pinned Node version once, then run:

```bash
vp env pin 24
cd web
vp install --frozen-lockfile
cd ..
make build
```

`make build` builds the React assets, copies them into the `go:embed` tree, and
creates `dist/megad`. Release builds set version, commit, and UTC build-time
metadata through linker flags; the values are available from
`GET /api/v1/version`.

The normal verification commands are:

```bash
go fmt ./...
go vet ./...
go mod verify
govulncheck ./...
go test ./... -count=1
go test -race ./... -count=1
cd web && vp install --frozen-lockfile && vp check && vp test && vp build
cd ..
make check
make test
make build
make audit
make graceful-shutdown
make resource-benchmark
make production-smoke
make docker-smoke
```

The live compatibility smoke is opt-in and must use only a maintainer-owned,
project-safe public fixture:

```bash
MEGADW_LIVE_MEGA_URL='https://mega.nz/file/...' make test-live
```

## Local operation

```bash
./dist/megad
```

The default listener is `127.0.0.1:8080`. Open `/setup` on first launch and
create the local administrator. Application state defaults to `/var/lib/megad`.
Transfer storage has no default: both roots start empty and downloads remain
disabled until `incompleteRoot` and `completeRoot` are explicitly configured
in Settings. The roots may be any valid absolute paths selected by the
operator. They are persisted in SQLite, while each job also retains the roots
it was created with; upgrades never move existing files or rewrite persisted
job paths.

Keep the state directory separate from transfer storage. The incomplete and
complete roots must remain on the same filesystem because completion is a
single atomic rename; a cross-filesystem configuration is rejected at
finalization rather than copied.

For an unprivileged local run, set `-state-dir`, `-database`, and
`-secret-key` to writable paths. `MEGAD_LISTEN`, `MEGAD_STATE_DIR`,
`MEGAD_DATABASE`, and `MEGAD_SECRET_KEY` are equivalent environment overrides.
When an HTTPS reverse proxy is the browser entry point, set
`MEGAD_SECURE_COOKIES=true` (or pass `-secure-cookies`) so the administrator
session cookie is never sent over plain HTTP. The listener address and local
interface addresses are allowed automatically; add a reverse-proxy DNS name
with `MEGAD_ALLOWED_HOSTS=downloads.example.test` (comma-separated for more
than one). Requests with any other browser `Host` are rejected on unsafe
methods, and `X-Forwarded-*` headers are not trusted.
`-mega-api-base` exists for deterministic compatibility fixtures and routed
deployments; normal releases use MEGA's production API endpoint.

## Docker / Docker Compose (primary)

The production image is a multi-stage build whose final layer contains one
static Go binary with the React assets embedded. Node.js, Java, the frontend
package manager, and build tools are absent from the runtime image. The image
runs as UID/GID `65532:65532`, drops capabilities, uses a read-only root
filesystem, exposes a built-in healthcheck, and handles SIGTERM within the
normal 20-second application shutdown bound.

Copy the checked-in example, replace every placeholder with an operator-chosen
path and immutable image digest, then validate and start it:

```bash
cp packaging/megad.compose.env.example .env
$EDITOR .env
docker compose -f compose.yaml config --quiet
docker compose -f compose.yaml up -d
```

Set `MEGAD_ALLOWED_HOSTS` to the exact browser-visible host and port. The
example uses `127.0.0.1:8080`; update it when changing `MEGAD_PORT` or placing
the service behind a named reverse proxy.

The Compose example uses separate host paths for state and transfer storage.
It mounts one transfer parent into the container so the two configured roots
can be sibling directories on one filesystem. After `/setup`, configure both
roots in the authenticated Settings page using their container-visible paths;
Compose does not infer or write either setting. Grant UID `65532` access to
the selected host directories before starting the service. Keep the published
listener loopback-only unless a trusted reverse proxy or controlled network
exposure is intended.

Use `docker compose stop` or `docker compose down` for a graceful SIGTERM
shutdown. Replacing the image leaves the state and transfer host paths intact,
and persisted settings and job roots are read with the same semantics as a
native installation. `docker compose` never requires privileged mode.

## Native Linux binary + systemd (secondary)

The supported native service is `packaging/megad.service`:

```bash
groupadd --system media
useradd --system --home-dir /var/lib/megad --no-create-home --shell /usr/sbin/nologin --gid media megad
install -d -o megad -g media -m 0750 /var/lib/megad
install -d -m 0755 /etc/megad
install -m 0644 packaging/megad.env.example /etc/megad/megad.env
install -m 0755 dist/megad /usr/local/bin/megad
install -m 0644 packaging/megad.service /etc/systemd/system/megad.service
systemctl daemon-reload
systemctl enable --now megad
```

The unit runs as `megad:media`, stores its secret and SQLite database under
`/var/lib/megad`, and starts safely with transfer roots unconfigured. It keeps
outbound networking enabled, but uses `NoNewPrivileges`, private temporary
storage, strict system protection, no ambient or bounding capabilities, kernel
protection, namespace restrictions, and a 20-second stop timeout. Inspect the
checked-in contract with `sh scripts/audit-systemd.sh`.

After choosing and creating the two transfer roots, grant only those paths to
the service in a systemd drop-in. For example, replace the placeholders below
with the exact configured roots:

```ini
# systemctl edit megad
[Service]
ReadWritePaths=
ReadWritePaths=/var/lib/megad /absolute/path/chosen-by-operator/partial /absolute/path/chosen-by-operator/complete
```

The drop-in is the native equivalent of the Compose bind-mount permission
boundary. It lets `ProtectSystem=strict` remain enabled while supporting
arbitrary operator-selected roots. Configure the same two paths in the
authenticated Settings page; do not put transfer files under the state
directory.

## Optional LXC / Proxmox environment

An LXC or Proxmox guest is an optional host for the native service. Prefer the
native systemd installation above; running Docker inside LXC additionally
requires an intentionally configured nesting-capable guest and is not needed
by MEGA Downloader itself.

Bind-mount one host transfer parent into the guest so incomplete and complete
remain on the same filesystem. For example, after stopping container `101`, a
Proxmox host can add an operator-selected mount with:

```bash
pct set 101 -mp0 /host/path/chosen-by-operator/transfer,mp=/guest/path/transfer
```

Create the two roots below that guest-visible parent, configure those exact
paths in Settings, and add them to the native systemd `ReadWritePaths` drop-in.
For an unprivileged LXC, map or ACL the host ownership so the guest's
`megad:media` identity can write the mount; do not solve permission errors with
world-writable directories or a privileged application container. State may
remain inside the guest at `/var/lib/megad`, or be mounted separately for the
operator's backup policy. No fixed host or guest transfer path is required.

## Resource profiles and release benchmark

The minimum supported profile is 2 vCPU and 4 GiB RAM. The upper target is 4
vCPU and 8 GiB RAM. The minimum profile is the release acceptance boundary: the
downloader-only process must remain comfortably below 512 MiB RSS under the
default eight-worker stress fixture, while the normal idle and eight-worker
targets are 80 MiB and 180 MiB RSS respectively. The upper profile is used to
validate additional parallelism, not to loosen the minimum guarantees.

Run the deterministic release matrix with the fake MEGA server in a separate
process:

```bash
make resource-benchmark
```

The command prints one JSON record for each required worker count. It records
RSS, CPU utilization, throughput, peak file descriptors, peak goroutines,
SQLite write/checkpoint transaction rates, and peak temporary disk overhead.
Use `taskset -c 0-1` for a true two-CPU affinity on a host with four or more
CPUs; the harness also applies a 4-GiB/8-GiB virtual-address limit when a
writable child memory cgroup is unavailable. For infrastructure validation,
run it inside a cgroup or container with `CPUQuota=200%` and `MemoryMax=4G` (or the
4-vCPU/8-GiB equivalent). The benchmark reports its constraint note and never
labels an unconstrained host run as a constrained-profile result.

The release measurement recorded for this checkout is maintained below. CPU
affinity was applied where available; this environment did not permit creating
a writable child memory cgroup, so the memory figures are host measurements,
not fabricated constrained-profile results.

| Profile | Workers | Throughput | Max RSS | CPU / target CPUs | Max FDs | Max goroutines | SQLite writes/s | Checkpoints/s | Temp overhead |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| small | 1 | 47.93 MiB/s | 22.70 MiB | 4.96% | 13 | 11 | 0.562 | 0.562 | 0.75 MiB |
| small | 4 | 155.41 MiB/s | 21.45 MiB | 16.39% | 16 | 18 | 0.607 | 0.607 | 0.39 MiB |
| small | 8 | 245.29 MiB/s | 27.04 MiB | 26.83% | 20 | 30 | 0.958 | 0.958 | 0.30 MiB |
| large | 8 | 244.43 MiB/s | 27.85 MiB | 15.75% | 20 | 30 | 0.955 | 0.955 | 0.30 MiB |
| large | 12 | 280.66 MiB/s | 27.36 MiB | 18.36% | 24 | 42 | 1.096 | 1.096 | 0.32 MiB |

Defaults remain the plan's safe values: 4 workers per file, 2 active files,
and 8 global workers. Change them only when the complete matrix demonstrates
effectively equivalent throughput with materially lower resource use.

## Security and release hygiene

MEGA credentials and sessions, public-link keys, and proxy passwords are
AES-256-GCM encrypted at rest. Local administrator session tokens are stored
only as SHA-256 digests. Secret material is redacted from normal logs.
Browser-supplied paths are relative and root-confined; existing symlinks in
controlled paths are rejected.
Configured roots are held as directory capabilities, and nested opens,
deletion, verification, and finalization use descriptor-relative Linux
operations so parent-path replacement cannot redirect work outside them.

`THIRD_PARTY_NOTICES.md` lists direct runtime/build dependencies.
`scripts/license-audit.sh` checks the pinned Go and npm dependency metadata,
`scripts/audit-systemd.sh` checks the native service hardening contract, and
`make docker-smoke` checks the image build, non-root runtime, healthcheck,
SIGTERM shutdown, and state persistence.

The production browser smoke builds and runs the embedded binary, performs
setup/login, exercises the API and SSE connection, directly reloads dashboard,
downloads, and settings routes, and confirms that the binary spawns neither
Node.js nor Java:

```bash
make production-smoke
```

## License

The project is licensed under GPL-3.0-only. See [LICENSE](LICENSE),
[NOTICE.md](NOTICE.md), and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
