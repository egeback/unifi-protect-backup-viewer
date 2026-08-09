#!/usr/bin/env bash
# Deploy unifi-protect-backup-viewer to Marvin.
#
# Registry push to Gitea is unreliable right now (KAN-443, the same issue
# documented for soc/smart-charging) — so like those two, this builds the
# image directly on the target host instead of push/pull through the
# registry. Marvin is x86_64 so there's no cross-arch concern.
#
# This talks to Marvin's plain docker daemon directly over SSH — it does
# NOT touch the docker-infrastructure repo or wait on Hawster. Use it for
# fast :dev iteration. Once you're happy, commit+push
# docker-infrastructure/marvin/unifi-protect-backup-viewer/compose.yaml
# separately so Hawster takes over long-term (same container_name, so it
# just adopts/recreates the container Hawster's next sync run).
set -euo pipefail

HOST="root@10.0.1.12"
REMOTE_DIR="/mnt/flash/container_data/unifi-protect-backup-viewer/app"
IMAGE="gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer:dev"

VERIFY_SYMBOL=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--verify)
		VERIFY_SYMBOL="$2"
		shift 2
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 1
		;;
	esac
done

echo "==> rsyncing repo to ${HOST}:${REMOTE_DIR}"
ssh "$HOST" "mkdir -p ${REMOTE_DIR}"
rsync -az --delete \
	--exclude .git \
	--exclude data \
	--exclude '*.db' \
	--exclude '*.db-wal' \
	--exclude '*.db-shm' \
	./ "${HOST}:${REMOTE_DIR}/"

echo "==> docker build on ${HOST}"
ssh "$HOST" "cd ${REMOTE_DIR} && docker build -t ${IMAGE} ."

if [[ ! -f "$(dirname "$0")/../.env.deployed.marker" ]]; then
	echo "==> NOTE: expecting ${REMOTE_DIR}/.env to already exist on Marvin (created manually, never in git)."
fi

echo "==> docker compose up -d --force-recreate --pull never"
ssh "$HOST" "cd ${REMOTE_DIR} && docker compose up -d --force-recreate --pull never"

ssh "$HOST" "docker ps --filter name=unifi-protect-backup-viewer"

if [[ -n "$VERIFY_SYMBOL" ]]; then
	echo "==> verifying symbol '${VERIFY_SYMBOL}' landed in the built image"
	COUNT=$(ssh "$HOST" "docker run --rm --entrypoint sh ${IMAGE} -c 'strings ./unifi-protect-backup-viewer | grep -c \"${VERIFY_SYMBOL}\" || true'")
	if [[ "$COUNT" -lt 1 ]]; then
		echo "!! symbol '${VERIFY_SYMBOL}' NOT found in built image — rsync/build likely picked up stale files" >&2
		exit 1
	fi
	echo "==> verified: found ${COUNT} occurrence(s)"
fi

echo "==> done"
