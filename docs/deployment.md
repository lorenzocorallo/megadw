# Deployment and operations

Docker Compose is the primary production deployment. A native Linux binary
with systemd is also supported.

## Docker Compose

The production image contains only the static `megadw` binary, its embedded
web interface, and CA certificates. It runs as UID/GID `65532:65532` and does
not contain Node.js, Java, a shell, or build tools.

Copy the example environment file and replace every placeholder with an
operator-chosen path and an immutable image digest:

```bash
cp packaging/megadw.compose.env.example .env
$EDITOR .env
docker compose config --quiet
docker compose up -d
```

The relevant files are [compose.yaml](../compose.yaml) and
[packaging/megadw.compose.env.example](../packaging/megadw.compose.env.example).

### Storage

The Compose file expects two host locations:

- `MEGADW_STATE_HOST_PATH` stores SQLite state and the encryption key.
- `MEGADW_TRANSFER_HOST_PATH` stores partial and completed downloads.

Do not put transfer storage inside the state directory. Mount one common
transfer parent into the container, then configure sibling incomplete and
complete roots in **Settings**. For example, with
`MEGADW_TRANSFER_CONTAINER_ROOT=/downloads`, use
`/downloads/incomplete` and `/downloads/complete`.

Both transfer roots must be on the same filesystem. Completion uses one atomic
rename, so megadw rejects cross-filesystem roots. Settings validation creates
missing roots and verifies that the service can write them.

Grant UID/GID `65532:65532` access to both host directories before starting
the container. Keep the published port bound to loopback unless a trusted
reverse proxy or controlled network provides access.

### Reverse proxy and TLS

Set `MEGADW_ALLOWED_HOSTS` to the exact browser-visible host and port. Use a
comma-separated list when more than one host is needed.

When TLS terminates at a reverse proxy, set:

```dotenv
MEGADW_ALLOWED_HOSTS=downloads.example.com
MEGADW_SECURE_COOKIES=true
```

This marks the administrator cookie as Secure and permits same-host HTTPS
origins. megadw does not trust `X-Forwarded-*` headers. Requests with an
unapproved browser `Host` are rejected on unsafe methods.

### Stop, upgrade, and backup

Stop the process through Compose so it receives SIGTERM and finishes its
bounded checkpoint and shutdown sequence:

```bash
docker compose down
```

To upgrade, change `MEGADW_IMAGE` to the new immutable release digest and run:

```bash
docker compose pull
docker compose up -d
```

For a consistent backup, stop the service and archive the entire state
directory, including `megadw.sqlite3`, any SQLite `-wal`/`-shm` files, and
`secret.key`. The database and key are one restore unit; losing the key makes
encrypted links, accounts, and proxy data unrecoverable.

Back up transfer storage separately. Restore both locations at the same
container-visible paths before starting megadw again.

## Native Linux binary and systemd

Build `dist/megadw` as described in [Development](development.md), then install
the supplied service:

```bash
groupadd --system media
useradd --system --home-dir /var/lib/megadw --no-create-home --shell /usr/sbin/nologin --gid media megadw
install -d -o megadw -g media -m 0750 /var/lib/megadw
install -d -m 0755 /etc/megadw
install -m 0644 packaging/megadw.env.example /etc/megadw/megadw.env
install -m 0755 dist/megadw /usr/local/bin/megadw
install -m 0644 packaging/megadw.service /etc/systemd/system/megadw.service
systemctl daemon-reload
systemctl enable --now megadw
```

The unit runs as `megadw:media`, keeps its state under `/var/lib/megadw`, and
starts with transfer roots unconfigured. It enables strict system protection,
no new privileges, private temporary storage, no Linux capabilities, and a
20-second stop timeout.

After choosing the transfer roots, create them with restricted ownership:

```bash
install -d -o megadw -g media -m 0750 /path/to/incomplete /path/to/complete
```

Then add their exact paths to a systemd drop-in:

```ini
# systemctl edit megadw
[Service]
ReadWritePaths=
ReadWritePaths=/var/lib/megadw /path/to/incomplete /path/to/complete
```

```bash
systemctl restart megadw
```

Configure the same paths in **Settings**. Keep both
transfer roots on one filesystem. The hardening contract can be checked with:

```bash
sh scripts/audit-systemd.sh
```

## LXC and Proxmox

Prefer the native systemd installation inside an LXC or Proxmox guest. Docker
inside LXC requires a nesting-capable guest and is not required by megadw.

Bind-mount one host transfer parent into the guest so incomplete and complete
remain on the same filesystem. For example, after stopping container `101`:

```bash
pct set 101 -mp0 /host/path/to/transfers,mp=/guest/path/to/transfers
```

For an unprivileged LXC, map ownership or grant an ACL so the guest's
`megadw:media` identity can write the mount. Do not use world-writable
directories or a privileged application container as a permissions workaround.

## Runtime configuration notes

Application state defaults to `/var/lib/megadw`. Transfer storage has no
default; downloads remain disabled until both roots are configured.

Speed limits, retry policy, checkpoint policy, read-idle timeout, and workers
per file apply to new work after saving. Restart megadw gracefully after
changing global concurrency or connect/response-header timeouts. Existing jobs
retain their persisted roots and segment layout.

For an unprivileged local run, use `-state-dir`, `-database`, and `-secret-key`
with writable paths. Equivalent environment variables are
`MEGADW_STATE_DIR`, `MEGADW_DATABASE`, and `MEGADW_SECRET_KEY`.
`MEGADW_LISTEN` changes the listener address.

The `-mega-api-base` option exists for deterministic test fixtures and routed
deployments. Normal releases use MEGA's production API endpoint.
