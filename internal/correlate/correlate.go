// Package correlate matches indexed clips to real UniFi Protect smart-detect
// events when available, and falls back to a duration-based guess otherwise.
//
// Real event types can only be captured for clips recorded after this
// service started listening (see internal/protect) — there is no historical
// event-search API. Clips older than that, or recorded while the listener
// was down, only ever get the heuristic classification.
package correlate

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer/internal/db"
	"gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer/internal/filenameparse"
	"gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer/internal/protect"
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

	if err := c.db.InsertEvent(ev.CameraID, cameraKey, ev.Type, ev.Start, ev.End, ev.RawJSON); err != nil {
		c.log.Error("storing protect event failed", "error", err)
		return
	}
	c.log.Info("stored protect event", "camera_key", cameraKey, "type", ev.Type, "start", ev.Start)
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
		eventType, found, err := database.FindOverlappingEvent(clip.CameraKey, clip.Start, clip.End, overlapTolerance)
		if err != nil {
			log.Error("finding overlapping event failed", "clip_id", clip.ID, "error", err)
			continue
		}
		if found {
			if err := database.SetClipClassification(clip.ID, eventType, "protect"); err != nil {
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
		if err := database.SetClipClassification(clip.ID, guess, "heuristic"); err != nil {
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
