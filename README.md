# UniFi Protect NAS-klipp Viewer

Ett Protect-liknande webb-GUI för de klipp UniFi Protect redan exporterar
till `/mnt/tank/nvr/UniFi` på Marvin (TrueNAS). Bläddra, filtrera och spela
upp — ingen egen inspelningslogik, bara läsning av det som redan finns.

## Vad det gör

- Indexerar `UniFi-Protect_YYYY-MM-DD/*.mp4`-mapparna och läser kamera +
  start-/sluttid direkt ur filnamnet.
- Lyssnar på Protects realtids-websocket för riktiga smart-detect-händelser
  (person/fordon/djur/...) och kopplar dem till nya klipp. Äldre klipp får
  en grov gissning baserad på klipplängd — se [DEPLOY.md](DEPLOY.md) för
  varför en riktig bakåt-backfill inte är möjlig.
- Transkodar klipp (4K HEVC) on-demand till H.264 via Intel QuickSync
  (VAAPI) på Marvins iGPU, så de går att spela i Chrome/Firefox och inte
  bara Safari.
- Enkel inloggning grindar hela appen.

## Utveckling

```bash
go build ./...
go test ./...
go run ./cmd/hashpw '<lösenord>'   # generera AUTH_PASSWORD_HASH
```

Kräver `ffmpeg` i PATH för thumbnail-generering/transkodning lokalt; för
riktig VAAPI-hårdvaruakselerering krävs Marvins `/dev/dri` — se
[DEPLOY.md](DEPLOY.md).

## Arkitektur i korthet

Go-binär (embeddar det statiska frontend-bygget), SQLite, ingen extern
databas. Se [DEPLOY.md](DEPLOY.md) för deploy-flödet och `spec.MD` för den
fullständiga designplanen.
