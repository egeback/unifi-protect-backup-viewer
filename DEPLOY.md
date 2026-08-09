# Deploying

## The simple way

If you can run Docker Compose directly on the machine that has both the
NVR export mount and (optionally) `/dev/dri` for hardware transcoding:

```bash
cp .env.example .env
# edit .env — see comments in the file
go run ./cmd/hashpw '<your chosen password>'   # paste the output into AUTH_PASSWORD_HASH
openssl rand -hex 32                            # paste the output into SESSION_SECRET

docker compose up --build -d
```

Edit `compose.yaml` first if your NVR export lives somewhere other than
`/mnt/tank/nvr/UniFi`, or if you don't have an Intel iGPU for VAAPI
transcoding (drop the `/dev/dri` mount and `group_add` lines — playback
still works, it just won't be hardware-accelerated).

## `scripts/deploy.sh`

If you'd rather build on a remote host over SSH than run Compose there
directly (e.g. a NAS you'd rather not `cd` into by hand every time), see
the comment header in `scripts/deploy.sh` — it's a small rsync + remote
`docker build` + `docker compose up` helper, configured entirely through
`DEPLOY_HOST`/`DEPLOY_REMOTE_DIR`/`DEPLOY_IMAGE` environment variables:

```bash
DEPLOY_HOST=user@your-nas.example \
DEPLOY_REMOTE_DIR=/opt/unifi-protect-backup-viewer \
  scripts/deploy.sh --verify <some_symbol_from_your_change>
```

`--verify <symbol>` greps the built image's binary for a string you know
you just added/changed, so a silent rsync/build miss (deploying stale code)
surfaces immediately instead of shipping unnoticed.

## Operational notes

- Real Protect smart-detect event types (person/vehicle/animal/...) are
  only captured **going forward** from whenever the event listener has been
  running — there's no historical event-search API in UniFi Protect's
  Integration API as of writing. Clips indexed before that, or during any
  listener downtime, keep the duration-based heuristic classification
  (check the `event_source` column — `"heuristic"` vs `"protect"` — if a
  clip's badge looks wrong).
- Transcoded proxy files are cached under `/data/proxies` and pruned after
  `TRANSCODE_CACHE_TTL` (default 14 days) — safe to delete by hand too,
  hardware transcode is cheap to redo.
- If you mount `/dev/dri`, the container's supplementary groups need to
  include your host's `video` and `render` GIDs (`getent group video
  render`) or playback in Chrome/Firefox will fail. Safari can still play
  the raw HEVC directly via `/api/clips/:id/download`, which is useful for
  telling "VAAPI broken" apart from "app broken".
