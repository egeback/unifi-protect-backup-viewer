# UniFi Protect NAS Clip Viewer

A Protect-timeline-style web UI for browsing clips UniFi Protect has
already exported to a NAS share (Protect Settings → Storage → "Backup to
network share"). Browse, filter and play them back — this doesn't record
anything itself, it's purely a viewer for what's already on disk.

## Why this exists

UniFi Protect can back up recordings to an SMB/NFS share, but gives you no
way to browse that backup — just a flat pile of files named like:

```text
Front Door 8-8-2026, 2.04.06pm GMT+2 - 8-8-2026, 2.04.27pm GMT+2.mp4
```

This app indexes that folder tree and gives you a timeline/grid view
instead, similar to Protect's own UI.

## Features

- Indexes `UniFi-Protect_YYYY-MM-DD/*.mp4` folders and reads camera name +
  start/end time straight out of the filename — no separate database of
  your camera layout to maintain.
- Listens to Protect's real-time Integration API event WebSocket to tag new
  clips with actual smart-detect types (person / vehicle / animal / face /
  license plate / audio alarms). A single detection can carry several types
  at once (e.g. face+person+vehicle+licensePlate for one car arriving) —
  the clip's badge shows the most specific one, but filtering by any of the
  co-occurring types still finds it, matching how Protect's own UI counts
  the same event under multiple categories.
- Optional historical backfill (needs a local UniFi OS admin account, see
  [DEPLOY.md](DEPLOY.md)) fills in real event types for clips from before
  the listener started, or during any downtime, using Protect's own
  session-authenticated events API — the Integration API has no historical
  event-search endpoint, so this is the only way. This same API is also the
  only source for a license plate reading or a recognized "Known Face"
  name, shown alongside the badge when available.
- Transcodes clips on demand (UniFi exports 4K HEVC, which Chrome/Firefox
  won't play natively) — uses Intel QuickSync (VAAPI) hardware
  transcoding if `/dev/dri` is available, with a disk cache so repeat
  views are instant.
- Login (session cookie) gates the whole app — one username/password per
  person, everyone with equal full access; no roles or per-user permissions.
- One Go binary (frontend embedded via `embed.FS`), SQLite for storage —
  no separate database or Node build step to run.

## Quick start

```bash
cp app.env.example app.env
# edit app.env
go run ./cmd/hashpw '<your chosen password>'   # -> an entry in AUTH_USERS
openssl rand -hex 32                            # -> SESSION_SECRET

docker compose up --build -d
```

See [DEPLOY.md](DEPLOY.md) for details, including running without hardware
transcoding if you don't have an Intel iGPU.

## Development

```bash
go build ./...
go test ./...
```

Needs `ffmpeg` on `PATH` for thumbnail generation / transcoding when
running outside the container.

## Known limitations

- No retention/deletion management of the original clips; read-only.
- No per-user permissions — every logged-in user sees everything.
- Filename parsing assumes the format UniFi Protect's SMB export currently
  uses; if Ubiquiti changes it, `internal/filenameparse` is the place to
  fix it.
- Backfill and the license plate/face-name detail both depend on an
  undocumented, session-authenticated Protect API — it works today but
  isn't an officially supported integration surface, so it could change
  without notice.

## License

MIT — see [LICENSE](LICENSE).
