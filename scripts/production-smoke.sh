#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BINARY=${1:-$ROOT/dist/megadw}
PORT=${MEGADW_SMOKE_PORT:-$((18080 + ($$ % 1000)))}
BASE_URL="http://127.0.0.1:$PORT"
RUN_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/megadw-production-smoke.XXXXXX")
LOG_FILE="$RUN_ROOT/megadw.log"
PID=""

cleanup() {
	if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
		kill -TERM "$PID" 2>/dev/null || true
		for _ in $(seq 1 200); do
			if ! kill -0 "$PID" 2>/dev/null || [ "$(awk '{print $3}' "/proc/$PID/stat" 2>/dev/null || true)" = "Z" ]; then
				break
			fi
			sleep 0.1
		done
		if kill -0 "$PID" 2>/dev/null; then
			kill -KILL "$PID" 2>/dev/null || true
		fi
	fi
	if [ -n "$PID" ]; then
		wait "$PID" 2>/dev/null || true
	fi
	rm -rf "$RUN_ROOT"
}
trap cleanup EXIT INT TERM

test -x "$BINARY"

"$BINARY" \
	-listen "127.0.0.1:$PORT" \
	-state-dir "$RUN_ROOT/state" \
	-database "$RUN_ROOT/state/megadw.sqlite3" \
	-secret-key "$RUN_ROOT/state/secret.key" \
	>"$LOG_FILE" 2>&1 &
PID=$!

ready=0
for _ in $(seq 1 200); do
	if curl --fail --silent "$BASE_URL/api/v1/health" >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 0.1
done
if [ "$ready" -ne 1 ]; then
	cat "$LOG_FILE" >&2
	echo 'production binary did not become healthy' >&2
	exit 1
fi

if ps -eo ppid=,comm= | awk -v parent="$PID" '$1 == parent { print $2 }' | grep -E '^(node|java|nodejs|java)$' >/dev/null 2>&1; then
	echo 'production binary spawned a Node.js or Java runtime' >&2
	exit 1
fi

(cd "$ROOT/web" && MEGADW_BASE_URL="$BASE_URL" vp exec playwright test --config=e2e/production.config.ts e2e/production.spec.ts)

kill -TERM "$PID"
exit_code=0
for _ in $(seq 1 200); do
	if ! kill -0 "$PID" 2>/dev/null || [ "$(awk '{print $3}' "/proc/$PID/stat" 2>/dev/null || true)" = "Z" ]; then
		break
	fi
	sleep 0.1
done
if kill -0 "$PID" 2>/dev/null && [ "$(awk '{print $3}' "/proc/$PID/stat" 2>/dev/null || true)" != "Z" ]; then
	echo 'production binary exceeded the 20-second shutdown bound' >&2
	exit_code=1
fi
wait "$PID" 2>/dev/null || true
PID=""
if [ "$exit_code" -ne 0 ]; then
	cat "$LOG_FILE" >&2
	exit "$exit_code"
fi

echo 'production embedded-binary browser smoke: PASS'
