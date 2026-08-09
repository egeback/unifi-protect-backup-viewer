Ready for review
Select text to add comments on the plan
UniFi Protect NAS-klipp Viewer
Context
UniFi Protect säkerhetskopierar inspelningar till /mnt/tank/nvr/UniFi/UniFi-Protect_YYYY-MM-DD/*.mp4 på Marvin (TrueNAS). Det finns inget sätt att bläddra i dessa klipp idag förutom att leta i filsystemet. Målet är ett eget webb-GUI, likt Protects egen tidslinje, sorterat på datum/tid och (om möjligt) händelsetyp.

Repo-katalogen unifi-protect-backup-viewer/ finns redan skapad (tom) på repo-nivå i home-lab, i linje med hur soc/ och smart-charging/ är egna repos med en motsvarande deploy-post i docker-infrastructure/.

Vad research-fasen visade (viktigt för designen)
Filnamnsformat: {Kamera} [- <ev. suffix>] M-D-YYYY, H.MM.SSam/pm GMT+2 - M-D-YYYY, H.MM.SSam/pm GMT+2.mp4 Ex: Baksidan - Frrdet 8-8-2026, 2.04.06pm GMT+2 - 8-8-2026, 2.04.27pm GMT+2.mp4. "Frrdet" är kamerans namn "Baksidan - Förrådet" med å/ä/ö bortstrippade av SMB-exporten (verifierat mot Protect API — kameran heter faktiskt "Baksidan - Förrådet"). Ger oss: kamera, starttid, sluttid direkt ur filnamnet. Ingen sidecar-metadata finns.
Video-codec: kontrollerat med ffprobe — klippen är 4K HEVC (hvc1) + AAC. Spelas inte upp nativt i Chrome/Firefox (bara Safari). Måste transkodas on-demand.
Marvin har Intel N305 iGPU (/dev/dri/card0 + /dev/dri/renderD128, grupper video=GID 44, render=GID 107) → hårdvaru-transkodning via VAAPI (QuickSync) är möjlig, vilket håller CPU-kostnaden låg (perf-budget-krav).
Protect Integration API funkar med er befintliga UNIFI_API_KEY mot https://10.0.6.2/proxy/protect/integration/v1/ (X-API-KEY header, testat och verifierat — GET /cameras fungerar). Men: det finns inget REST-endpoint för historisk händelsesökning (/events → 404 NOT_FOUND). Endast en realtids-websocket (/subscribe/events) finns. Konsekvens: riktiga smart-detect-typer (person/fordon/djur) kan bara samlas in framåt i tiden via en lyssnare vi bygger — inte bakåt för klipp som redan ligger på disk. De får en filnamns-/duration-baserad gissning istället.
Bara en kamera är registrerad idag ("Baksidan - Förrådet"). Den äldre etiketten "G6 Bullet" i filnamn från 5 aug är samma fysiska kamera innan den döptes om.
Gitea container-registry push är sannolikt trasig (samma orsak som dokumenterad för soc/smart-charging: KAN-443, gitea API-token autentiserar inte mot registryt). Deploy följer därför samma validerade mönster: bygg direkt på target-hosten (Marvin, x86_64 — inget cross-arch-behov) via rsync + docker build, inte docker push.
Arkitektur
En ny standalone-app, Go, i unifi-protect-backup-viewer/ (eget repo):

Backend: Go. En binär, embeddar det statiska frontend-bygget (embed.FS) — inget separat Node/webpack-steg, ingen extra container.
Databas: SQLite (en fil på persistent volym) — rätt skala för ett hem-NVR-arkiv, ingen separat DB-tjänst behövs.
Frontend: Vanilla JS/CSS SPA (ingen React/build-pipeline), Protect-liknande layout:
Sidopanel: kameralista + datumväljare (byggd direkt från UniFi-Protect_YYYY-MM-DD-mapparna)
Filterrad: händelsetyp-chips (Alla / Person / Fordon / Djur / Rörelse / Kontinuerlig / Okänd)
Huvudyta: kronologiskt thumbnail-grid för valt datum, tid + varaktighet + typ-badge
Klick → modal-spelare med föregående/nästa-navigering inom dagen, knapp för att ladda ner originalfilen (ej transkodad)
Enkel inloggningssida (session-cookie) som grindar hela appen — ett delat användarnamn/lösenord (bcrypt-hash i env, inte i git)
Komponenter i backend
Indexer — går igenom /nvr/UniFi-Protect_*/*.mp4 periodiskt (t.ex. var 5:e min)
filsystembevakning för nya filer. Parsar filnamn → kamera, start, slut, duration. Hoppar över filer vars mtime är för färskt (Protect kan fortfarande skriva). Skriver/uppdaterar rad i clips-tabellen (idempotent på filsökväg).
Protect event-lyssnare — bakgrundsgorutin som prenumererar på /proxy/protect/integration/v1/subscribe/events (samma API-nyckel). Lagrar riktiga smart-detect-händelser (kamera, typ, start, slut) i en events-tabell.
Correlator — för varje ny/oklassad klipp-rad: leta efter en events-rad från samma kamera (normaliserat namn, prefix-match p.g.a. filnamnstrunkering) med överlappande tidsfönster. Match → sätt riktig event_type + event_source=protect. Ingen match → filnamns-/duration-heuristik (kort klipp + suffix ⇒ "Rörelse", långt utan suffix ⇒ "Kontinuerlig", annars "Okänd") + event_source=heuristic.
Thumbnail-generering — ett ffmpeg -ss <mitten> -vframes 1-anrop per klipp vid första indexering, cachas till disk.
Video-streaming (/api/clips/:id/stream):
Finns redan en transkodad cache-fil på disk → servas direkt med http.ServeContent (Range-stöd, snabb sökning).
Annars: kör ffmpeg med VAAPI hw-accel (-hwaccel vaapi ... -c:v h264_vaapi), skriv fragmenterad MP4 (empty_moov+frag_keyframe) samtidigt till HTTP-svaret (progressiv uppspelning direkt) och till en cache-fil via io.MultiWriter. Nästa uppspelning av samma klipp är då redan cachead och Range-sökbar.
Nattlig cleanup-jobb som rensar cache-filer äldre än t.ex. 14 dagar (billigt att transkoda om med hw-accel).
Auth-middleware — session-cookie (HMAC-signerad), enkel login-route, gate på alla routes utom /login och statiska assets.
/health-endpoint — JSON-svar, samma mönster som unifi-backup använder för Gatus-integration.
Datamodell (SQLite)
clips(id, path, camera_name, start_ts, end_ts, duration_s, size_bytes, mtime, event_type, event_source, thumbnail_ready, indexed_at)
events(id, camera_id, camera_name, type, start_ts, end_ts, raw_json, received_at) — pruned efter t.ex. 90 dagar
Deploy
App-repo unifi-protect-backup-viewer/: Dockerfile (multi-stage: bygg Go-binär med embeddat frontend → final debian-slim + ffmpeg + intel-media-va-driver), compose.yaml (lokal/dev), DEPLOY.md, scripts/deploy.sh — modellerat exakt efter soc/scripts/deploy.sh:
rsync repo → Marvin /tmp/unifi-protect-backup-viewer-build/ (foreground, exkl. .git/data)
docker build -t gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer:dev . på Marvin
docker compose up -d --force-recreate --pull never i deploy-katalogen på Marvin
--verify <symbol>-grep mot körande container, så en tyst rsync/build-miss syns direkt
Skriptat, inte bara körbara engångskommandon (dokumenteras i DEPLOY.md) — enligt tidigare bekräftad preferens om att script:a återkommande ops
Deploy-manifest docker-infrastructure/marvin/unifi-protect-backup-viewer/compose.yaml:
volumes: /mnt/tank/nvr/UniFi:/nvr:ro, /mnt/flash/container_data/unifi-protect-backup-viewer/data:/data
devices: ["/dev/dri:/dev/dri"], group_add: ["44", "107"] (video/render GID)
env_file: /mnt/flash/container_data/unifi-protect-backup-viewer/.env — innehåller PROTECT_API_KEY, PROTECT_HOST=10.0.6.2, AUTH_USER, AUTH_PASSWORD_HASH, SESSION_SECRET (skapas manuellt på Marvin, aldrig committat)
Traefik-labels modellerade på jellyfin/compose.yaml (t.ex. nvr.internal.egeback.se)
gatus.enabled=true-labels mot /health, samma mönster som unifi-backup
Exakt var Hawster checkar ut repot på Marvin (compose-katalogens absoluta path) behöver bekräftas mot en körande service under implementationen — inte antaget här.
Explicit avgränsning (för att inte över-bygga v1)
Ingen bakåt-backfill av riktiga Protect-händelsetyper för befintliga klipp — bara framåt i tiden, plus grov filnamns-heuristik för resten.
Ingen radering/retention-hantering av originalklippen i v1 — bara läsning.
Ett delat login, ingen multi-user/roller.
Verifiering
docker compose config validerar deploy-manifestet.
Kör scripts/deploy.sh --verify <symbol> mot Marvin, bekräfta docker ps + att image-Created-tiden är efter senaste commit.
curl mot /health → 200, och att Gatus plockar upp den.
Öppna GUI:t i Chrome (inte bara Safari) och spela ett befintligt HEVC-klipp → bekräftar att VAAPI-transkodningen faktiskt fungerar, inte bara i teorin.
Kontrollera att indexeraren hittat samma antal klipp som find /mnt/tank/nvr/UniFi -name '*.mp4' | wc -l.
Trigga en riktig rörelse framför "Baksidan - Förrådet"-kameran, bekräfta att det nya klippet inom några minuter dyker upp i GUI:t med en riktig Protect-händelsetyp (inte bara heuristik).
Bekräfta att inloggningsgrinden faktiskt blockerar oautentiserad åtkomst.