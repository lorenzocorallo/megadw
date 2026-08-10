#!/bin/sh
set -eu

BINARY=${1:?benchmark binary is required}
FIXTURE=${2:?fixture binary is required}
ONLINE_CPUS=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 0)

run_with_constraints() {
	profile=$1
	affinity=$2
	memory_limit_kib=$3
	shift 2
	shift
	required_cpus=$(( ${affinity##*-} + 1 ))
	(
		# The cgroup hierarchy is the preferred release constraint. When it is
		# unavailable, cap virtual address space as a reproducible fallback;
		# the benchmark still reports host RSS rather than calling this LXC.
		ulimit -v "$memory_limit_kib" 2>/dev/null || true
		if command -v taskset >/dev/null 2>&1 && [ "$ONLINE_CPUS" -ge "$required_cpus" ] 2>/dev/null; then
			MEGADW_RESOURCE_CONSTRAINT="taskset CPU affinity $affinity; virtual memory limit ${memory_limit_kib}KiB; no child cgroup memory limit was available" taskset -c "$affinity" "$BINARY" --profile "$profile" --fixture-binary "$FIXTURE" "$@"
		else
			MEGADW_RESOURCE_CONSTRAINT="virtual memory limit ${memory_limit_kib}KiB; no reproducible child CPU affinity or memory cgroup was available" "$BINARY" --profile "$profile" --fixture-binary "$FIXTURE" "$@"
		fi
	)
}

run_profile() {
	profile=$1
	shift
	case "$profile" in
		small) run_with_constraints small 0-1 4194304 "$@" ;;
		large) run_with_constraints large 0-3 8388608 "$@" ;;
		*) MEGADW_RESOURCE_CONSTRAINT="no reproducible child CPU affinity or memory cgroup was available" "$BINARY" --profile "$profile" --fixture-binary "$FIXTURE" "$@" ;;
	esac
}

if [ "${MEGADW_RESOURCE_PROFILE:-all}" = "small" ]; then
	run_profile small
elif [ "${MEGADW_RESOURCE_PROFILE:-all}" = "large" ]; then
	run_profile large
else
	run_profile small
	run_profile large
fi
