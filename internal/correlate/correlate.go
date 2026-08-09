// Package correlate matches indexed clips to real UniFi Protect smart-detect
// events when available, and falls back to a duration-based guess otherwise.
//
// Live-classified events come from the Integration API's event WebSocket
// (see internal/protect), which only ever sees events from whenever it
// started listening onward. Backfill (this package's Backfill method) fills
// in everything else — clips recorded before the listener started, or
// during any downtime — using the legacy session-authenticated events API,
// which does support historical search (unlike the Integration API).
package correlate

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/egeback/unifi-protect-backup-viewer/internal/db"
	"github.com/egeback/unifi-protect-backup-viewer/internal/filenameparse"
	"github.com/egeback/unifi-protect-backup-viewer/internal/protect"
)

const (
	// overlapTolerance absorbs small clock/boundary differences between the
	// NAS export's timestamps and Protect's own event timestamps.
	overlapTolerance = 5 * time.Second

	// gracePeriod is how long we wait after a clip's end time before giving
	// up on a matching Protect event and falling back to the heuristic —
	// long enough to outlast normal websocket/indexer latency.
	gracePeriod = 10 * time.Minute

	shortClipMaxDuration = 3 * time.Minute
	longClipMinDuration  = 5 * time.Minute

	EventTypeContinuous = "continuous"
	EventTypeMotion     = "motion"
	EventTypeUnknown    = "unknown"
)

type Correlator struct {
	db     *db.DB
	client *protect.Client
	log    *slog.Logger

	mu        sync.RWMutex
	cameraKey map[string]string // Protect camera ID -> normalized camera_key
}

func New(database *db.DB, client *protect.Client, log *slog.Logger) *Correlator {
	return &Correlator{db: database, client: client, log: log, cameraKey: map[string]string{}}
}

// RefreshCameraDirectory pulls the current camera list so incoming events
// (keyed by Protect's internal camera ID) can be mapped to the same
// normalized camera_key the indexer derives from filenames.
func (c *Correlator) RefreshCameraDirectory() error {
	cams, err := c.client.Cameras()
	if err != nil {
		return err
	}
	m := make(map[string]string, len(cams))
	for _, cam := range cams {
		m[cam.ID] = filenameparse.NormalizeCameraName(cam.Name)
	}
	c.mu.Lock()
	c.cameraKey = m
	c.mu.Unlock()
	return nil
}

func (c *Correlator) lookupCameraKey(cameraID string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	k, ok := c.cameraKey[cameraID]
	return k, ok
}

// OnEvent stores a real-time Protect smart-detect event for later matching
// against clips. Intended as the callback passed to protect.Listen.
func (c *Correlator) OnEvent(ev protect.SmartDetectEvent) {
	cameraKey, ok := c.lookupCameraKey(ev.CameraID)
	if !ok {
		if err := c.RefreshCameraDirectory(); err != nil {
			c.log.Warn("refreshing camera directory failed", "error", err)
		}
		cameraKey, ok = c.lookupCameraKey(ev.CameraID)
	}
	if !ok {
		c.log.Warn("dropping event for unknown camera id", "camera_id", ev.CameraID)
		return
	}

	if err := c.db.InsertEvent(ev.ID, ev.CameraID, cameraKey, ev.Type, ev.Detail, ev.Types, ev.Start, ev.End, ev.RawJSON); err != nil {
		c.log.Error("storing protect event failed", "error", err)
		return
	}
	c.log.Info("stored protect event", "camera_key", cameraKey, "type", ev.Type, "start", ev.Start)
}

// Backfill fetches historical events from the earliest still-not-protect-
// classified clip's start time up to now, stores them, and re-runs
// classification for every clip — including ones already protect-sourced,
// in case a type-derivation fix (like PickPrimaryType's priority ordering)
// means the same underlying event now resolves to a better answer. Safe to
// call repeatedly (e.g. on a periodic timer): with nothing left to backfill
// it's just one cheap DB query, and reclassification skips any clip whose
// stored classification already matches what's found.
func (c *Correlator) Backfill(ctx context.Context, legacy *protect.LegacyClient) error {
	from, ok, err := c.db.EarliestNonProtectClipStart()
	if err != nil {
		return err
	}
	if !ok {
		return nil // every clip already has a real Protect match
	}
	to := time.Now()

	events, err := legacy.Events(ctx, from, to)
	if err != nil {
		return err
	}

	if err := c.RefreshCameraDirectory(); err != nil {
		c.log.Warn("refreshing camera directory before backfill failed", "error", err)
	}

	var stored, unknownCamera int
	for _, ev := range events {
		cameraKey, ok := c.lookupCameraKey(ev.Camera)
		if !ok {
			unknownCamera++
			continue
		}
		start := time.UnixMilli(ev.Start)
		var end *time.Time
		if ev.End != nil {
			t := time.UnixMilli(*ev.End)
			end = &t
		}
		raw, _ := json.Marshal(ev)
		if err := c.db.InsertEvent(ev.ID, ev.Camera, cameraKey, ev.PrimaryType, ev.Detail, ev.Types, start, end, string(raw)); err != nil {
			c.log.Error("storing backfilled event failed", "error", err)
			continue
		}
		stored++
	}

	upgraded, err := c.reclassifyAllClips()
	if err != nil {
		return err
	}

	c.log.Info("backfill complete",
		"from", from, "to", to, "events_fetched", len(events),
		"events_stored", stored, "unknown_camera", unknownCamera, "clips_changed", upgraded)
	return nil
}

// reclassifyAllClips re-checks every clip against the events table,
// updating it if the best available match differs from what's currently
// stored (whether that's upgrading a heuristic guess or correcting an
// earlier protect match after a type-derivation fix).
func (c *Correlator) reclassifyAllClips() (int, error) {
	clips, err := c.db.ListClips(db.ClipFilter{})
	if err != nil {
		return 0, err
	}

	var changed int
	for _, clip := range clips {
		eventType, detail, types, found, err := c.db.FindOverlappingEvent(clip.CameraKey, clip.Start, clip.End, overlapTolerance)
		if err != nil {
			c.log.Error("finding overlapping event failed", "clip_id", clip.ID, "error", err)
			continue
		}
		if !found {
			continue
		}
		if clip.EventSource == "protect" && clip.EventType == eventType && clip.EventDetail == detail && slices.Equal(clip.EventTypes, types) {
			continue // already correct
		}
		if err := c.db.SetClipClassification(clip.ID, eventType, "protect", detail, types); err != nil {
			c.log.Error("updating classification failed", "clip_id", clip.ID, "error", err)
			continue
		}
		changed++
	}
	return changed, nil
}

// RunClassifier periodically classifies clips that don't have a real
// Protect event match yet, applying the heuristic once the grace period for
// a real match has passed.
func RunClassifier(ctx context.Context, database *db.DB, log *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		classifyOnce(database, log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func classifyOnce(database *db.DB, log *slog.Logger) {
	clips, err := database.UnclassifiedClips()
	if err != nil {
		log.Error("listing unclassified clips failed", "error", err)
		return
	}

	var matched, heuristic, deferred int
	for _, clip := range clips {
		eventType, detail, types, found, err := database.FindOverlappingEvent(clip.CameraKey, clip.Start, clip.End, overlapTolerance)
		if err != nil {
			log.Error("finding overlapping event failed", "clip_id", clip.ID, "error", err)
			continue
		}
		if found {
			if err := database.SetClipClassification(clip.ID, eventType, "protect", detail, types); err != nil {
				log.Error("setting classification failed", "clip_id", clip.ID, "error", err)
				continue
			}
			matched++
			continue
		}

		if time.Since(clip.End) < gracePeriod {
			deferred++
			continue // still might get a real event
		}

		guess := heuristicType(time.Duration(clip.DurationS) * time.Second)
		if err := database.SetClipClassification(clip.ID, guess, "heuristic", "", []string{guess}); err != nil {
			log.Error("setting heuristic classification failed", "clip_id", clip.ID, "error", err)
			continue
		}
		heuristic++
	}

	if matched+heuristic > 0 {
		log.Info("classification pass complete", "matched_protect", matched, "heuristic", heuristic, "deferred", deferred)
	}
}

func heuristicType(duration time.Duration) string {
	switch {
	case duration >= longClipMinDuration:
		return EventTypeContinuous
	case duration <= shortClipMaxDuration:
		return EventTypeMotion
	default:
		return EventTypeUnknown
	}
}
