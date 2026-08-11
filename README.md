# megadw

megadw is a self-hosted web app for resumable public MEGA file and folder
downloads. It runs as one Go process with the web interface built in.

> [!WARNING]
> megadw is experimental, was developed with substantial AI assistance, and
> has not received a complete independent security or reliability audit. Keep
> backups, give it access only to its own storage, and do not expose it directly
> to the public internet.

## Deploy with Docker Compose

Docker Compose is the recommended way to run megadw.

1. Create a state directory and a transfer directory. The container runs as
   UID/GID `65532:65532`, so both must be writable by that user:

   ```bash
   sudo install -d -o 65532 -g 65532 /srv/megadw/state /srv/megadw/transfers
   ```

2. Copy the example configuration:

   ```bash
   cp packaging/megadw.compose.env.example .env
   ```

   To run the current checkout instead of a published release, first build a
   local image with `docker build -t megadw:local .`, then use
   `MEGADW_IMAGE=megadw:local` below. Production deployments should use an
   immutable release digest.

3. Edit `.env` with the image digest for the release you want to run and these
   paths:

   ```dotenv
   MEGADW_IMAGE=ghcr.io/lorenzocorallo/megadw@sha256:<release-digest>
   MEGADW_ALLOWED_HOSTS=127.0.0.1:8080
   MEGADW_STATE_HOST_PATH=/srv/megadw/state
   MEGADW_TRANSFER_HOST_PATH=/srv/megadw/transfers
   MEGADW_TRANSFER_CONTAINER_ROOT=/downloads
   ```

4. Start it:

   ```bash
   docker compose up -d
   ```

Open [http://127.0.0.1:8080/setup](http://127.0.0.1:8080/setup), create the
administrator account, then set these download locations in **Settings**:

- Incomplete root: `/downloads/incomplete`
- Complete root: `/downloads/complete`

The checked-in [compose.yaml](compose.yaml) is the complete production example.
It runs read-only and non-root, drops Linux capabilities, includes a
healthcheck, and keeps application state separate from downloaded files.

To stop the service cleanly:

```bash
docker compose down
```

For reverse-proxy/TLS configuration, backups, upgrades, native systemd setup,
and LXC/Proxmox notes, see [Deployment and operations](docs/deployment.md).

## What it supports

- Public MEGA file and folder links
- Resumable, restart-safe downloads
- Parallel downloads with bounded workers and bandwidth controls
- MEGA integrity verification before completion
- Optional standard MEGA accounts without MFA/2FA
- HTTP, HTTPS CONNECT, and SOCKS5 proxy profiles
- A local administrator account and browser-based settings

Uploads, streaming, cloud browsing, MFA/2FA account login, and automatic
quota-evasion features are not part of the current version. Quota responses
pause the selected job; megadw does not rotate identities to bypass limits.

## Run from source

The project uses Go 1.26.5, Node.js 24.19.0, Vite+ 0.2.6, and TypeScript 7.0.2.

```bash
vp env pin 24
cd web && vp install --frozen-lockfile && cd ..
make build
./dist/megadw -state-dir "$PWD/.megadw"
```

The binary listens on `127.0.0.1:8080` by default. See
[Development](docs/development.md) for the full build, test, and release
commands.

## Documentation

- [Deployment and operations](docs/deployment.md)
- [Development](docs/development.md)
- [Technical notes](docs/technical-notes.md)
- [Implementation and release gates](PLAN.md)
- [Deferred upload plan](.docs/plans/PLAN-UPLOAD.md)
- [Deferred streaming plan](.docs/plans/PLAN-STREAMING.md)

## License

GPL-3.0-only. See [LICENSE](LICENSE), [NOTICE.md](NOTICE.md), and
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
