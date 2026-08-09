// Command unifi-protect-backup-viewer serves a Protect-like browsing UI for
// clips UniFi Protect has already exported to a NAS share.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/egeback/unifi-protect-backup-viewer/internal/api"
	"github.com/egeback/unifi-protect-backup-viewer/internal/auth"
	"github.com/egeback/unifi-protect-backup-viewer/internal/config"
	"github.com/egeback/unifi-protect-backup-viewer/internal/correlate"
	"github.com/egeback/unifi-protect-backup-viewer/internal/db"
	"github.com/egeback/unifi-protect-backup-viewer/internal/indexer"
	"github.com/egeback/unifi-protect-backup-viewer/internal/protect"
	"github.com/egeback/unifi-protect-backup-viewer/internal/stream"
	"github.com/egeback/unifi-protect-backup-viewer/internal/thumbnail"
	"github.com/egeback/unifi-protect-backup-viewer/web"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.DataPath, 0o755); err != nil {
		return err
	}

	database, err := db.Open(cfg.DataPath + "/nvr-viewer.db")
	if err != nil {
		return err
	}
	defer database.Close()

	thumbs, err := thumbnail.New(database, cfg.DataPath, log)
	if err != nil {
		return err
	}
	streamer, err := stream.New(cfg.DataPath, log)
	if err != nil {
		return err
	}
	authMgr := auth.New(cfg.AuthUser, cfg.AuthPasswordHash, cfg.SessionSecret)

	server := api.NewServer(database, streamer, thumbs, authMgr, log)
	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: server.Routes(web.Static()),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	idx := indexer.New(database, cfg.NVRPath, cfg.IndexInterval, log)
	go func() {
		if err := idx.Run(ctx); err != nil {
			log.Error("indexer stopped", "error", err)
		}
	}()

	go correlate.RunClassifier(ctx, database, log, time.Minute)

	go runPeriodically(ctx, 2*time.Minute, func() { thumbs.RunPending(ctx) })

	go runPeriodically(ctx, 6*time.Hour, func() { streamer.CleanCache(cfg.TranscodeCacheTTL) })

	if cfg.ProtectAPIKey != "" && cfg.ProtectHost != "" {
		client := protect.NewClient(cfg.ProtectHost, cfg.ProtectAPIKey, cfg.ProtectInsecureSkipVerify)
		correlator := correlate.New(database, client, log)
		if err := correlator.RefreshCameraDirectory(); err != nil {
			log.Warn("initial protect camera directory fetch failed", "error", err)
		}
		go runPeriodically(ctx, 10*time.Minute, func() {
			if err := correlator.RefreshCameraDirectory(); err != nil {
				log.Warn("refreshing protect camera directory failed", "error", err)
			}
		})
		go protect.Listen(ctx, client, log, correlator.OnEvent)

		if cfg.ProtectUser != "" && cfg.ProtectPassword != "" {
			legacy := protect.NewLegacyClient(cfg.ProtectHost, cfg.ProtectUser, cfg.ProtectPassword)
			go runPeriodically(ctx, 15*time.Minute, func() {
				if err := correlator.Backfill(ctx, legacy); err != nil {
					log.Error("backfill failed", "error", err)
				}
			})
		} else {
			log.Warn("PROTECT_USER/PROTECT_PASSWORD not set — clips missed by the live event listener will never get upgraded past the heuristic guess")
		}
	} else {
		log.Warn("PROTECT_API_KEY/PROTECT_HOST not set — clips will only get heuristic event classification")
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Error("http server shutdown error", "error", err)
		}
	}()

	log.Info("listening", "addr", cfg.ListenAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// runPeriodically calls fn immediately, then again every interval, until
// ctx is cancelled. The immediate call matters here: thumbnail generation
// otherwise sat idle for a full interval after every startup/restart before
// doing anything.
func runPeriodically(ctx context.Context, interval time.Duration, fn func()) {
	fn()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn()
		}
	}
}
