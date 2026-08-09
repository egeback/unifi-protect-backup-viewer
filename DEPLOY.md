# Deploying

## The simple way

If you can run Docker Compose directly on the machine that has both the
NVR export mount and (optionally) `/dev/dri` for hardware transcoding:

```bash
cp app.env.example app.env
# edit app.env — see comments in the file
go run ./cmd/hashpw '<your chosen password>' | sed 's/\$/\$\$/g'   # -> AUTH_PASSWORD_HASH (see note below)
openssl rand -hex 32                                                 # -> SESSION_SECRET

docker compose up --build -d
```

The `sed` above isn't optional: Compose interpolates `$VAR` references
inside `env_file` values, so a raw bcrypt hash's `$` characters get
silently eaten as "undefined variable" references — corrupting the hash
into something that will never match any password. Doubling every `$` to
`$$` is how you get a literal `$` through. If login mysteriously never
works, `docker compose up` printing `"... variable is not set"` warnings
for chunks of your hash is the tell; `docker exec <container> sh -c 'echo
$AUTH_PASSWORD_HASH'` to compare against what `hashpw` actually printed.

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
- The container runs as an unprivileged user (UID 1000), not root. If
  `./data` doesn't already exist, Docker creates it as root when the
  container first starts, and the app then fails to open its SQLite
  database (`unable to open database file`, crash-looping). Create it
  yourself first and hand it to the right owner:
  `mkdir -p data && chown 1000:1000 data`.
