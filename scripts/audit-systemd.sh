#!/bin/sh
set -eu

unit=${1:-packaging/megad.service}
test -f "$unit"

required='User=megad
Group=media
StateDirectory=megad
Restart=on-failure
RestartSec=5s
TimeoutStopSec=20s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
CapabilityBoundingSet=
AmbientCapabilities=
ReadWritePaths=/var/lib/megad'

printf '%s\n' "$required" | while IFS= read -r line; do
	if ! grep -Fqx "$line" "$unit"; then
		echo "missing systemd hardening directive: $line" >&2
		exit 1
	fi
done

if grep -Eq '^PrivateNetwork=(true|yes|1)$' "$unit"; then
	echo 'PrivateNetwork must remain disabled: MEGA downloads need outbound networking' >&2
	exit 1
fi

read_write_paths=$(grep -Ec '^ReadWritePaths=' "$unit")
if [ "$read_write_paths" -ne 1 ]; then
	echo 'systemd unit must contain only its state ReadWritePaths allow-list; add transfer roots in a drop-in' >&2
	exit 1
fi

echo "systemd hardening contract: PASS ($unit)"
