// Package indexer scans the UniFi Protect NAS export tree and keeps the
// clips table in sync with what's actually on disk.
package indexer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/egeback/unifi-protect-backup-viewer/internal/db"
	"github.com/egeback/unifi-protect-backup-viewer/internal/filenameparse"

	"github.com/fsnotify/fsnotify"
)

var dayFolderPattern = regexp.MustCompile(`^UniFi-Protect_(\d{4}-\d{2}-\d{2})$`)

// minAge is how long a file must be untouched before we trust it's a
// finished write, not a clip Protect is still exporting.
const minAge = 30 * time.Second

type Indexer struct {
	db       *db.DB
	nvrPath  string
	interval time.Duration
	log      *slog.Logger

	// OnNewClips is called with the number of newly-seen clips after each
	// scan, letting the caller kick off dependent work (thumbnails, correlation).
	OnNewClips func(count int)
}

func New(database *db.DB, nvrPath string, interval time.Duration, log *slog.Logger) *Indexer {
	return &Indexer{db: database, nvrPath: nvrPath, interval: interval, log: log}
}

// Run performs an initial scan, then rescans periodically and whenever
// fsnotify observes filesystem activity under nvrPath, until ctx is cancelled.
func (idx *Indexer) Run(ctx context.Context) error {
	idx.scanOnce(ctx)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		idx.log.Warn("fsnotify unavailable, falling back to periodic-only scanning", "error", err)
		watcher = nil
	} else {
		defer watcher.Close()
		idx.watchTree(watcher)
	}

	ticker := time.NewTicker(idx.interval)
	defer ticker.Stop()

	// Debounce fsnotify bursts: wait for a quiet period before rescanning.
	var debounce *time.Timer
	debounceC := make(chan struct{})
	resetDebounce := func() {
		if debounce == nil {
			debounce = time.AfterFunc(10*time.Second, func() { debounceC <- struct{}{} })
		} else {
			debounce.Reset(10 * time.Second)
		}
	}

	var events <-chan fsnotify.Event
	var errs <-chan error
	if watcher != nil {
		events = watcher.Events
		errs = watcher.Errors
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			idx.scanOnce(ctx)
		case ev := <-events:
			if ev.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				if watcher != nil && ev.Op&fsnotify.Create != 0 {
					if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
						_ = watcher.Add(ev.Name)
					}
				}
				resetDebounce()
			}
		case err := <-errs:
			idx.log.Warn("fsnotify error", "error", err)
		case <-debounceC:
			idx.scanOnce(ctx)
		}
	}
}

func (idx *Indexer) watchTree(watcher *fsnotify.Watcher) {
	if err := watcher.Add(idx.nvrPath); err != nil {
		idx.log.Warn("watching NVR root failed", "path", idx.nvrPath, "error", err)
		return
	}
	entries, err := os.ReadDir(idx.nvrPath)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && dayFolderPattern.MatchString(e.Name()) {
			_ = watcher.Add(filepath.Join(idx.nvrPath, e.Name()))
		}
	}
}

// scanOnce walks every day folder and upserts each clip it finds.
func (idx *Indexer) scanOnce(ctx context.Context) {
	start := time.Now()
	entries, err := os.ReadDir(idx.nvrPath)
	if err != nil {
		idx.log.Error("reading NVR root failed", "path", idx.nvrPath, "error", err)
		return
	}

	var scanned, upserted, skipped, parseErrors int
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if !entry.IsDir() {
			continue
		}
		m := dayFolderPattern.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		day := m[1]
		dayPath := filepath.Join(idx.nvrPath, entry.Name())

		files, err := os.ReadDir(dayPath)
		if err != nil {
			idx.log.Error("reading day folder failed", "path", dayPath, "error", err)
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".mp4") {
				continue
			}
			scanned++

			info, err := f.Info()
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) < minAge {
				skipped++
				continue // still being written
			}

			parsed, err := filenameparse.Parse(f.Name())
			if err != nil {
				parseErrors++
				idx.log.Warn("skipping unparseable filename", "file", f.Name(), "error", err)
				continue
			}

			clip := db.Clip{
				Path:       filepath.Join(dayPath, f.Name()),
				Day:        day,
				CameraName: parsed.CameraName,
				CameraKey:  filenameparse.NormalizeCameraName(parsed.CameraName),
				Start:      parsed.Start,
				End:        parsed.End,
				DurationS:  int64(parsed.End.Sub(parsed.Start).Seconds()),
				SizeBytes:  info.Size(),
				MTime:      info.ModTime(),
				// Explicit, not left to the SQL column DEFAULT: an INSERT
				// that lists the column always uses the Go zero value ("")
				// rather than falling through to DEFAULT, and the
				// correlator's UnclassifiedClips() query specifically
				// matches event_source = 'unknown' — leaving this as ""
				// would make every clip permanently invisible to it.
				EventType:   "unknown",
				EventSource: "unknown",
			}
			if _, err := idx.db.UpsertClip(clip); err != nil {
				idx.log.Error("upserting clip failed", "path", clip.Path, "error", err)
				continue
			}
			upserted++
		}
	}

	idx.log.Info("scan complete",
		"scanned", scanned, "upserted", upserted, "skipped_recent", skipped,
		"parse_errors", parseErrors, "took", time.Since(start))

	if idx.OnNewClips != nil && upserted > 0 {
		idx.OnNewClips(upserted)
	}
}
