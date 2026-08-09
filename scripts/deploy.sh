#!/usr/bin/env bash
# Maintainer's personal deploy helper: builds the image directly on a
# remote Docker host over SSH and runs it via this repo's compose.yaml.
#
# Most self-hosters won't need this at all — `docker compose up --build`
# on the target machine directly is simpler if you can just run Docker
# Compose there. This script exists for the case of deploying to a NAS/host
# where you'd rather build+push from your workstation than work on the box
# itself directly, and where a container registry isn't in the loop (e.g.
# your registry push is broken, or you just don't want one in the loop).
set -euo pipefail

HOST="${DEPLOY_HOST:?set DEPLOY_HOST, e.g. user@your-nas.example}"
REMOTE_DIR="${DEPLOY_REMOTE_DIR:?set DEPLOY_REMOTE_DIR, e.g. /opt/unifi-protect-backup-viewer}"
IMAGE="${DEPLOY_IMAGE:-unifi-protect-backup-viewer:local}"

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

echo "==> NOTE: expecting ${REMOTE_DIR}/.env to already exist on the host (created manually, never in git)."

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
