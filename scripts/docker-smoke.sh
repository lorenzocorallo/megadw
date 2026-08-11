#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DOCKER=${DOCKER:-docker}
IMAGE=${MEGADW_DOCKER_IMAGE:-megadw:docker-smoke}
PORT=${MEGADW_DOCKER_PORT:-$((20080 + ($$ % 500)))}
RUN_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/megadw-docker-smoke.XXXXXX")
CONTAINER=megadw-docker-smoke-$$

cleanup() {
	"$DOCKER" rm --force "$CONTAINER" >/dev/null 2>&1 || true
	rm -rf "$RUN_ROOT"
}
trap cleanup EXIT INT TERM

command -v "$DOCKER" >/dev/null 2>&1
command -v curl >/dev/null 2>&1
mkdir -p "$RUN_ROOT/state" "$RUN_ROOT/transfer"
# The smoke test uses isolated temporary directories. Production deployments
# must grant UID 65532 access with normal host ownership/ACLs instead.
chmod 0777 "$RUN_ROOT/state" "$RUN_ROOT/transfer"

"$DOCKER" build --tag "$IMAGE" --build-arg VERSION=docker-smoke --build-arg COMMIT=smoke --build-arg BUILD_TIME=smoke "$ROOT"
"$DOCKER" run --detach --name "$CONTAINER" \
	--publish "127.0.0.1:$PORT:8080" \
	--read-only \
	--cap-drop ALL \
	--security-opt no-new-privileges:true \
	--env "MEGADW_ALLOWED_HOSTS=127.0.0.1:$PORT" \
	--tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
	--volume "$RUN_ROOT/state:/var/lib/megadw:rw" \
	--volume "$RUN_ROOT/transfer:/transfer:rw" \
	"$IMAGE" >/dev/null

for _ in $(seq 1 120); do
	if [ "$("$DOCKER" inspect --format '{{.State.Health.Status}}' "$CONTAINER" 2>/dev/null || true)" = healthy ]; then
		break
	fi
	sleep 1
done
if [ "$("$DOCKER" inspect --format '{{.State.Health.Status}}' "$CONTAINER")" != healthy ]; then
	"$DOCKER" logs "$CONTAINER" >&2
	echo 'Docker healthcheck did not become healthy' >&2
	exit 1
fi

if [ "$("$DOCKER" inspect --format '{{.Config.User}}' "$CONTAINER")" != "65532:65532" ]; then
	echo 'Docker image did not run as UID/GID 65532:65532' >&2
	exit 1
fi
if [ "$("$DOCKER" inspect --format '{{.HostConfig.Privileged}}' "$CONTAINER")" != false ]; then
	echo 'Docker smoke container is privileged' >&2
	exit 1
fi

health=$(curl --fail --silent --show-error \
	"http://127.0.0.1:$PORT/api/v1/health")
if ! printf '%s' "$health" | grep -q '"transferPathsConfigured":false'; then
	echo 'Docker fresh state unexpectedly configured transfer roots' >&2
	exit 1
fi
curl --fail --silent --show-error \
	-H "Origin: http://127.0.0.1:$PORT" \
	-H 'Content-Type: application/json' \
	-d '{"username":"admin","password":"docker smoke password"}' \
	"http://127.0.0.1:$PORT/api/v1/auth/setup" >/dev/null

"$DOCKER" stop --time 25 "$CONTAINER" >/dev/null
"$DOCKER" start "$CONTAINER" >/dev/null
for _ in $(seq 1 120); do
	if [ "$("$DOCKER" inspect --format '{{.State.Health.Status}}' "$CONTAINER" 2>/dev/null || true)" = healthy ]; then
		break
	fi
	sleep 1
done
if [ "$("$DOCKER" inspect --format '{{.State.Health.Status}}' "$CONTAINER")" != healthy ]; then
	"$DOCKER" logs "$CONTAINER" >&2
	echo 'Docker container was not healthy after restart' >&2
	exit 1
fi
if ! curl --fail --silent --show-error "http://127.0.0.1:$PORT/api/v1/auth/status" | grep -q '"setupRequired":false'; then
	echo 'Docker state was not persisted across restart' >&2
	exit 1
fi

echo 'Docker build/non-root/start/health/SIGTERM/persistence: PASS'
