# Deploy (UniFi Protect NAS-klipp Viewer)

## TL;DR

```bash
scripts/deploy.sh --verify <some_symbol_from_your_change>
```

Builds `:dev` directly on Marvin and recreates the container. Reads
`/mnt/tank/nvr/UniFi` (read-only) and needs `/dev/dri` for hardware
transcoding — both only exist on Marvin, so there's no meaningful way to run
this against production data from anywhere else.

## Why not the registry?

Same story as `soc`/`smart-charging` (see their `DEPLOY.md`, KAN-443):
pushing to `gitea.internal.egeback.com/egeback/...` from a Mac client is
unreliable right now. Until that's resolved, `:dev` deploys build the image
**on Marvin itself** — x86_64, so no cross-arch build needed either.

## What `scripts/deploy.sh` does

1. `rsync` the repo to `10.0.1.12:/mnt/flash/container_data/unifi-protect-backup-viewer/app/`
   (excludes `.git`, `data/`, `*.db*`).
2. `docker build -t gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer:dev .`
   on Marvin.
3. `docker compose up -d --force-recreate --pull never` from that same
   directory, using **this repo's own `compose.yaml`** — independent of
   `docker-infrastructure`/Hawster, for fast iteration.
4. Prints `docker ps` status.
5. If `--verify <symbol>` was passed: pulls a `strings` count of that symbol
   out of the built image's binary (Go embeds string literals directly, so
   this catches a stale rsync/build same way soc's Python-source grep does).

### First-time setup on Marvin

Before the first deploy, create the env file the compose file expects
(never committed):

```bash
ssh root@10.0.1.12 mkdir -p /mnt/flash/container_data/unifi-protect-backup-viewer/app
ssh root@10.0.1.12 'cat > /mnt/flash/container_data/unifi-protect-backup-viewer/app/.env' <<'EOF'
PROTECT_HOST=10.0.6.2
PROTECT_API_KEY=<same key as UNIFI_API_KEY>
AUTH_USER=<choose a username>
AUTH_PASSWORD_HASH=<output of: go run ./cmd/hashpw '<password>'>
SESSION_SECRET=<output of: openssl rand -hex 32>
EOF
```

## Manual equivalent (if the script isn't available)

```bash
rsync -az --delete --exclude .git --exclude data --exclude '*.db*' \
  ./ 10.0.1.12:/mnt/flash/container_data/unifi-protect-backup-viewer/app/

ssh 10.0.1.12 "cd /mnt/flash/container_data/unifi-protect-backup-viewer/app && \
  docker build -t gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer:dev ."

ssh 10.0.1.12 "cd /mnt/flash/container_data/unifi-protect-backup-viewer/app && \
  docker compose up -d --force-recreate --pull never"
```

## Two compose files, two purposes

- **`compose.yaml`** (this repo) — what `scripts/deploy.sh` runs. No Traefik
  labels, port published directly on `:8080`, data under `./data` relative
  to the rsynced app directory. For fast `:dev` iteration.
- **`docker-infrastructure/marvin/unifi-protect-backup-viewer/compose.yaml`**
  — the GitOps-managed manifest Hawster watches: Traefik labels, Gatus
  labels, and the "real" persistent data path
  (`/mnt/flash/container_data/unifi-protect-backup-viewer/data`, a level up
  from where this repo's own `.env`/`data` live during `:dev` testing).
  Same `container_name`, so once you push that file, Hawster's next sync
  just adopts/recreates the same container — no conflict, no double-deploy.

## Verifying deploy actually landed

```bash
ssh 10.0.1.12 "docker run --rm --entrypoint sh gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer:dev -c 'strings ./unifi-protect-backup-viewer | grep -c <new_symbol>'"
ssh 10.0.1.12 "docker inspect gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer:dev --format='{{.Created}}'"
```

Compare the image `Created` timestamp against your last local commit/edit
time — if the image predates your change, it wasn't picked up.

## Operational notes

- Real Protect smart-detect event types (person/vehicle/animal/...) are only
  captured **going forward** from whenever the event listener has been
  running — there's no historical event-search API. Clips indexed before
  that, or during any listener downtime, keep the duration-based heuristic
  classification (`event_source = "heuristic"` vs `"protect"` in the DB —
  check that column if a clip's badge looks wrong).
- Transcoded proxy files are cached under `/data/proxies` and pruned after
  `TRANSCODE_CACHE_TTL` (default 14 days) — safe to delete by hand too,
  hardware transcode is cheap to redo.
- `/dev/dri` must be passed through and the container's supplementary
  groups must include Marvin's `video` (44) and `render` (107) GIDs, or
  playback in Chrome/Firefox will fail (Safari can still play the raw HEVC
  directly via `/api/clips/:id/download`, which is useful for telling
  "VAAPI broken" apart from "app broken").
