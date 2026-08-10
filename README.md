# MEGA Downloader

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
create the local administrator. The default state and download paths are:

```text
/var/lib/megad
/mnt/media/downloads/mega/incomplete
/mnt/media/downloads/mega/complete
```

For an unprivileged local run, set `-state-dir`, `-database`, and
`-secret-key` to writable paths. `MEGAD_LISTEN`, `MEGAD_STATE_DIR`,
`MEGAD_DATABASE`, and `MEGAD_SECRET_KEY` are equivalent environment overrides.
`-mega-api-base` exists for deterministic compatibility fixtures and routed
deployments; normal releases use MEGA's production API endpoint.

## Debian/Ubuntu Proxmox LXC deployment

The supported service is `packaging/megad.service`. In a Debian or Ubuntu LXC:

```bash
groupadd --system media
useradd --system --home-dir /var/lib/megad --no-create-home --shell /usr/sbin/nologin --gid media megad
install -d -o megad -g media -m 0750 /var/lib/megad
install -d -o megad -g media -m 0770 /mnt/media/downloads/mega/incomplete
install -d -o megad -g media -m 0770 /mnt/media/downloads/mega/complete
install -d -m 0755 /etc/megad
install -m 0644 packaging/megad.env.example /etc/megad/megad.env
install -m 0755 dist/megad /usr/local/bin/megad
install -m 0644 packaging/megad.service /etc/systemd/system/megad.service
systemctl daemon-reload
systemctl enable --now megad
```

The unit runs as `megad:media`, stores its secret and SQLite database under
`/var/lib/megad`, and can write only `/var/lib/megad` and
`/mnt/media/downloads/mega`. It keeps outbound networking enabled, but uses
`NoNewPrivileges`, private temporary storage, strict system protection, no
ambient or bounding capabilities, kernel protection, namespace restrictions,
and a 20-second stop timeout. Inspect the checked-in contract with
`sh scripts/audit-systemd.sh`.

For Proxmox, bind-mount the host media directory into the LXC at exactly
`/mnt/media`. Ensure the container's `megad` user and `media` group have write
permission on the two download directories. Keep the web listener on loopback
unless a reverse proxy, VPN, or explicitly controlled LAN address is intended;
bootstrap `/setup` before exposing it beyond the container. Do not rely on
untrusted `X-Forwarded-*` headers.

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
run it inside an LXC/cgroup with `CPUQuota=200%` and `MemoryMax=4G` (or the
4-vCPU/8-GiB equivalent). The benchmark reports its constraint note and never
labels an unconstrained host run as an LXC result.

The release measurement recorded for this checkout is maintained below. CPU
affinity was applied where available; this environment did not permit creating
a writable child memory cgroup, so the memory figures are host measurements,
not fabricated 4-GiB LXC results.

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

Application secrets are AES-256-GCM encrypted at rest; session tokens are only
stored as SHA-256 digests. Public-link fragments, passwords, proxy passwords,
and session material are redacted from normal logs. Browser-supplied paths are
relative and root-confined; existing symlinks in controlled paths are rejected.

`THIRD_PARTY_NOTICES.md` lists direct runtime/build dependencies.
`scripts/license-audit.sh` checks the pinned Go and npm dependency metadata,
and `scripts/audit-systemd.sh` checks the service hardening contract.

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
