#!/bin/sh
set -eu

BINARY=${1:?production binary is required}
BASE_PORT=${MEGADW_SHUTDOWN_PORT_BASE:-$((19080 + ($$ % 500)))}
RUN_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/megadw-shutdown-smoke.XXXXXX")

cleanup() {
	rm -rf "$RUN_ROOT"
}
trap cleanup EXIT INT TERM

test -x "$BINARY"

run_signal_case() {
	signal_name=$1
	port=$2
	root="$RUN_ROOT/$signal_name"
	log_file="$root/megadw.log"
	mkdir -p "$root"
	"$BINARY" \
		-listen "127.0.0.1:$port" \
		-state-dir "$root/state" \
		-database "$root/state/megadw.sqlite3" \
		-secret-key "$root/state/secret.key" \
		>"$log_file" 2>&1 &
	pid=$!
	ready=0
	for _ in $(seq 1 200); do
		if curl --fail --silent "http://127.0.0.1:$port/api/v1/health" >/dev/null 2>&1; then
			ready=1
			break
		fi
		sleep 0.1
	done
	if [ "$ready" -ne 1 ]; then
		cat "$log_file" >&2
		printf '%s\n' "${signal_name} process did not become healthy" >&2
		kill -KILL "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
		exit 1
	fi

	kill -"$signal_name" "$pid"
	for _ in $(seq 1 200); do
		if ! kill -0 "$pid" 2>/dev/null || [ "$(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null || true)" = "Z" ]; then
			break
		fi
		sleep 0.1
	done
	if kill -0 "$pid" 2>/dev/null && [ "$(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null || true)" != "Z" ]; then
		cat "$log_file" >&2
		printf '%s\n' "${signal_name} shutdown exceeded the 20-second bound" >&2
		kill -KILL "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
		exit 1
	fi
	wait "$pid" 2>/dev/null || true
	printf 'graceful %s shutdown: PASS\n' "$signal_name"
}

run_signal_case TERM "$BASE_PORT"
run_signal_case INT $((BASE_PORT + 1))
