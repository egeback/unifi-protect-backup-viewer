// Package stream serves clip video to browsers, transcoding on demand.
//
// UniFi Protect exports 4K HEVC, which Chrome/Firefox can't play natively.
// An Intel iGPU with VAAPI (QuickSync) support can hardware-transcode it,
// so the first request for a clip is decoded+encoded through /dev/dri and
// streamed straight to the response as fragmented MP4 (playable
// progressively, no seeking yet) while simultaneously being written to a
// disk cache. Every request after that is served straight from the cache
// file via http.ServeContent, which gives real Range/seek support.
package stream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/egeback/unifi-protect-backup-viewer/internal/db"
)

const (
	vaapiDevice      = "/dev/dri/renderD128"
	transcodeTimeout = 10 * time.Minute
)

type Streamer struct {
	cacheDir string
	log      *slog.Logger

	locksMu sync.Mutex
	locks   map[int64]*sync.Mutex
}

func New(dataDir string, log *slog.Logger) (*Streamer, error) {
	dir := filepath.Join(dataDir, "proxies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating proxy cache dir: %w", err)
	}
	return &Streamer{cacheDir: dir, log: log, locks: map[int64]*sync.Mutex{}}, nil
}

func (s *Streamer) cachePath(clipID int64) string {
	return filepath.Join(s.cacheDir, fmt.Sprintf("%d.mp4", clipID))
}

func (s *Streamer) clipLock(clipID int64) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	l, ok := s.locks[clipID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[clipID] = l
	}
	return l
}

// ServeOriginal streams the untouched source file (for downloads), with full Range support.
func ServeOriginal(w http.ResponseWriter, r *http.Request, clip db.Clip) {
	f, err := os.Open(clip.Path)
	if err != nil {
		http.Error(w, "source file unavailable", http.StatusNotFound)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "source file unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(clip.Path)))
	http.ServeContent(w, r, filepath.Base(clip.Path), fi.ModTime(), f)
}

// ServeProxy serves a browser-compatible H.264 version of the clip, from
// cache if available, otherwise transcoding on the fly.
func (s *Streamer) ServeProxy(w http.ResponseWriter, r *http.Request, clip db.Clip) {
	cachePath := s.cachePath(clip.ID)
	if s.serveCached(w, r, cachePath) {
		return
	}

	lock := s.clipLock(clip.ID)
	lock.Lock()
	defer lock.Unlock()

	// Another request may have finished transcoding while we waited for the lock.
	if s.serveCached(w, r, cachePath) {
		return
	}

	if err := s.transcodeAndServe(r.Context(), w, clip, cachePath); err != nil {
		s.log.Error("transcode failed", "clip_id", clip.ID, "path", clip.Path, "error", err)
	}
}

func (s *Streamer) serveCached(w http.ResponseWriter, r *http.Request, cachePath string) bool {
	f, err := os.Open(cachePath)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, filepath.Base(cachePath), fi.ModTime(), f)
	return true
}

func (s *Streamer) transcodeAndServe(ctx context.Context, w http.ResponseWriter, clip db.Clip, cachePath string) error {
	tmp := cachePath + ".tmp"
	tmpFile, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating temp proxy file: %w", err)
	}
	success := false
	defer func() {
		tmpFile.Close()
		if !success {
			os.Remove(tmp)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, transcodeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-hwaccel", "vaapi",
		"-hwaccel_device", vaapiDevice,
		"-hwaccel_output_format", "vaapi",
		"-i", clip.Path,
		"-vf", "scale_vaapi=w=1920:h=-2",
		"-c:v", "h264_vaapi",
		"-b:v", "6M",
		"-c:a", "aac",
		"-b:a", "128k",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4",
		"pipe:1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("attaching ffmpeg stdout: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	_, copyErr := io.Copy(io.MultiWriter(w, tmpFile), stdout)
	waitErr := cmd.Wait()

	if waitErr != nil {
		return fmt.Errorf("ffmpeg exited with error: %w: %s", waitErr, truncate(stderrBuf.Bytes(), 800))
	}
	if copyErr != nil {
		return fmt.Errorf("streaming ffmpeg output to client: %w", copyErr)
	}

	success = true
	tmpFile.Close()
	if err := os.Rename(tmp, cachePath); err != nil {
		return fmt.Errorf("promoting proxy cache file: %w", err)
	}
	return nil
}

// CleanCache removes cached proxy files whose last access predates the TTL.
// Safe to call periodically; re-transcoding is cheap with hardware accel.
func (s *Streamer) CleanCache(ttl time.Duration) {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		s.log.Warn("reading proxy cache dir failed", "error", err)
		return
	}
	cutoff := time.Now().Add(-ttl)
	var removed int
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(s.cacheDir, e.Name())); err == nil {
				removed++
			}
		}
	}
	if removed > 0 {
		s.log.Info("pruned proxy cache", "removed", removed)
	}
}

// truncate keeps the tail of ffmpeg's output, not the head — the useful
// error is always at the end, past the version/config banner.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return "..." + string(b[len(b)-n:])
}
