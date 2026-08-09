# Deploying

## The simple way

If you can run Docker Compose directly on the machine that has both the
NVR export mount and (optionally) `/dev/dri` for hardware transcoding:

```bash
cp app.env.example app.env
# edit app.env — see comments in the file
go run ./cmd/hashpw '<your chosen password>' | sed 's/\$/\$\$/g'   # -> one entry in AUTH_USERS (see note below)
openssl rand -hex 32                                                 # -> SESSION_SECRET

docker compose up --build -d
```

`AUTH_USERS` takes one `username:hash` pair per person, separated by `;`
(e.g. `marky:$$2a$$...;maria:$$2a$$...`) — everyone gets equal, full access,
there's no per-user permissions to configure. Add or remove an entry and
redeploy to add/revoke someone; an already-issued session cookie for a
removed user stops working on its next request, not just future logins.

The `sed` above isn't optional: Compose interpolates `$VAR` references
inside `env_file` values, so a raw bcrypt hash's `$` characters get
silently eaten as "undefined variable" references — corrupting the hash
into something that will never match any password. Doubling every `$` to
`$$` is how you get a literal `$` through. If login mysteriously never
works, `docker compose up` printing `"... variable is not set"` warnings
for chunks of your hash is the tell; `docker exec <container> sh -c 'echo
$AUTH_USERS'` to compare against what you actually put in `app.env`.

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

- Real Protect smart-detect event types (person/vehicle/animal/face/
  licensePlate/audio alarms) come from two sources. The Integration API's
  live event WebSocket only ever sees events from whenever it started
  listening onward — nothing historical (`GET /integration/v1/events` is a
  404, confirmed). If `PROTECT_USER`/`PROTECT_PASSWORD` are set, a periodic
  backfill job fills in everything else (and upgrades anything the live
  listener missed) using Protect's own undocumented, session-authenticated
  events API — the same one the Protect web UI itself uses, and the only
  place a license plate reading or a recognized face's name is exposed
  (`event_detail` in the API/DB). Without those credentials, clips before
  the app started (or from any listener downtime) keep the duration-based
  heuristic guess forever — check the `event_source` column
  (`"heuristic"` vs `"protect"`) if a clip's badge looks wrong.
- A single detection can carry multiple simultaneous types (e.g. a car
  triggers `face`+`person`+`vehicle`+`licensePlate` all at once, matching
  how Protect's own UI counts it under every one of those categories). The
  badge shown on a clip is the single most specific type
  (`internal/protect.PickPrimaryType`'s priority order), but filtering by
  any co-occurring type still matches — `event_type` is just the headline,
  `event_types` (JSON array) is what filtering actually checks.
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
