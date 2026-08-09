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
- Optionally listens to Protect's real-time Integration API event
  WebSocket to tag new clips with actual smart-detect types (person /
  vehicle / animal / ...). Clips that predate the listener (or that
  Protect's own retention has already aged out) fall back to a
  duration-based guess instead — see [DEPLOY.md](DEPLOY.md) for why a real
  historical backfill isn't possible with the API as it exists today.
- Transcodes clips on demand (UniFi exports 4K HEVC, which Chrome/Firefox
  won't play natively) — uses Intel QuickSync (VAAPI) hardware
  transcoding if `/dev/dri` is available, with a disk cache so repeat
  views are instant.
- Single shared login (session cookie) gates the whole app — this is a
  home-NVR viewer, not a multi-tenant product.
- One Go binary (frontend embedded via `embed.FS`), SQLite for storage —
  no separate database or Node build step to run.

## Quick start

```bash
cp app.env.example app.env
# edit app.env
go run ./cmd/hashpw '<your chosen password>'   # -> AUTH_PASSWORD_HASH
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

- No historical backfill of real Protect event types — see above.
- No retention/deletion management of the original clips; read-only.
- Single shared login, no multi-user/roles.
- Filename parsing assumes the format UniFi Protect's SMB export currently
  uses; if Ubiquiti changes it, `internal/filenameparse` is the place to
  fix it.

## License

MIT — see [LICENSE](LICENSE).
