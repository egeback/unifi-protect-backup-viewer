// Package thumbnail generates a single preview frame per clip via ffmpeg.
package thumbnail

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/egeback/unifi-protect-backup-viewer/internal/db"
)

const genTimeout = 30 * time.Second

type Generator struct {
	db  *db.DB
	dir string // dataDir/thumbnails
	log *slog.Logger
}

func New(database *db.DB, dataDir string, log *slog.Logger) (*Generator, error) {
	dir := filepath.Join(dataDir, "thumbnails")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating thumbnail dir: %w", err)
	}
	return &Generator{db: database, dir: dir, log: log}, nil
}

// Path returns where a clip's thumbnail lives (whether or not it exists yet).
func (g *Generator) Path(clipID int64) string {
	return filepath.Join(g.dir, fmt.Sprintf("%d.jpg", clipID))
}

// RunPending generates thumbnails for any clip that doesn't have one yet.
// Intended to be called periodically.
func (g *Generator) RunPending(ctx context.Context) {
	for {
		clips, err := g.db.ClipsWithoutThumbnail(20)
		if err != nil {
			g.log.Error("listing clips without thumbnail failed", "error", err)
			return
		}
		if len(clips) == 0 {
			return
		}
		for _, c := range clips {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if err := g.generate(ctx, c); err != nil {
				g.log.Warn("thumbnail generation failed", "clip_id", c.ID, "error", err)
				continue
			}
			if err := g.db.MarkThumbnailReady(c.ID); err != nil {
				g.log.Error("marking thumbnail ready failed", "clip_id", c.ID, "error", err)
			}
		}
	}
}

func (g *Generator) generate(ctx context.Context, c db.Clip) error {
	clip, err := g.db.ClipByID(c.ID)
	if err != nil {
		return err
	}

	mid := clip.End.Sub(clip.Start) / 2
	seek := fmt.Sprintf("%.2f", mid.Seconds())

	out := g.Path(c.ID)
	tmp := out + ".tmp"

	ctx, cancel := context.WithTimeout(ctx, genTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-ss", seek,
		"-i", clip.Path,
		"-frames:v", "1",
		"-update", "1",
		"-vf", "scale=480:-2",
		"-q:v", "4",
		tmp,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg thumbnail for %s failed: %w: %s", clip.Path, err, truncate(output, 500))
	}

	return os.Rename(tmp, out)
}

// truncate keeps the tail of ffmpeg's output, not the head — the useful
// error is always at the end, past the version/config banner.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return "..." + string(b[len(b)-n:])
}
