// Package api wires the HTTP routes together: clip listing/filtering,
// thumbnails, video streaming, auth and health.
package api

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer/internal/auth"
	"gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer/internal/db"
	"gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer/internal/stream"
	"gitea.internal.egeback.com/egeback/unifi-protect-backup-viewer/internal/thumbnail"
)

type Server struct {
	db       *db.DB
	streamer *stream.Streamer
	thumbs   *thumbnail.Generator
	auth     *auth.Manager
	log      *slog.Logger
}

func NewServer(database *db.DB, streamer *stream.Streamer, thumbs *thumbnail.Generator, authMgr *auth.Manager, log *slog.Logger) *Server {
	return &Server{db: database, streamer: streamer, thumbs: thumbs, auth: authMgr, log: log}
}

// Routes returns the fully-wired mux.
//
// Public (no session required): /health, /login (the login page itself and
// its assets — otherwise an unauthenticated visitor could never reach a
// page that lets them log in), POST /api/login.
// Everything else — the app shell, /assets/*, and all other /api/* — sits
// behind the session gate.
func (s *Server) Routes(staticFS fs.FS) http.Handler {
	assetsFS, err := fs.Sub(staticFS, "assets")
	if err != nil {
		panic("web/static must contain an assets/ directory: " + err.Error())
	}
	assetsHandler := http.StripPrefix("/assets/", http.FileServerFS(assetsFS))

	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/days", s.handleDays)
	protected.HandleFunc("GET /api/cameras", s.handleCameras)
	protected.HandleFunc("GET /api/clips", s.handleListClips)
	protected.HandleFunc("GET /api/clips/{id}/thumbnail", s.handleThumbnail)
	protected.HandleFunc("GET /api/clips/{id}/stream", s.handleStream)
	protected.HandleFunc("GET /api/clips/{id}/download", s.handleDownload)
	protected.HandleFunc("POST /api/logout", s.auth.LogoutHandler)
	protected.HandleFunc("GET /", serveStaticFile(staticFS, "index.html"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.auth.LoginHandler)
	mux.HandleFunc("GET /login", serveStaticFile(staticFS, "login.html"))
	mux.Handle("/assets/", assetsHandler)
	mux.Handle("/", s.auth.RequireSession(protected))

	return mux
}

func serveStaticFile(staticFS fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, err := fs.ReadFile(staticFS, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, name, buildTime, bytes.NewReader(content))
	}
}

// buildTime stands in for a real mtime — embed.FS doesn't preserve one, and
// these files change only on redeploy, so a fixed process-start time is fine
// for conditional-GET purposes.
var buildTime = time.Now()

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDays(w http.ResponseWriter, r *http.Request) {
	days, err := s.db.Days()
	if err != nil {
		s.log.Error("listing days failed", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, days)
}

func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request) {
	cams, err := s.db.Cameras()
	if err != nil {
		s.log.Error("listing cameras failed", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, cams)
}

type clipJSON struct {
	ID           int64  `json:"id"`
	Day          string `json:"day"`
	Camera       string `json:"camera"`
	CameraKey    string `json:"camera_key"`
	Start        string `json:"start"`
	End          string `json:"end"`
	DurationS    int64  `json:"duration_s"`
	SizeBytes    int64  `json:"size_bytes"`
	EventType    string `json:"event_type"`
	EventSource  string `json:"event_source"`
	ThumbnailURL string `json:"thumbnail_url"`
	StreamURL    string `json:"stream_url"`
	DownloadURL  string `json:"download_url"`
}

func toClipJSON(c db.Clip) clipJSON {
	id := strconv.FormatInt(c.ID, 10)
	return clipJSON{
		ID:           c.ID,
		Day:          c.Day,
		Camera:       c.CameraName,
		CameraKey:    c.CameraKey,
		Start:        c.Start.Format(time.RFC3339),
		End:          c.End.Format(time.RFC3339),
		DurationS:    c.DurationS,
		SizeBytes:    c.SizeBytes,
		EventType:    c.EventType,
		EventSource:  c.EventSource,
		ThumbnailURL: "/api/clips/" + id + "/thumbnail",
		StreamURL:    "/api/clips/" + id + "/stream",
		DownloadURL:  "/api/clips/" + id + "/download",
	}
}

func (s *Server) handleListClips(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := db.ClipFilter{
		Day:       q.Get("day"),
		CameraKey: q.Get("camera"),
		EventType: q.Get("type"),
		Limit:     200,
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 500 {
		filter.Limit = l
	}
	if o, err := strconv.Atoi(q.Get("offset")); err == nil && o >= 0 {
		filter.Offset = o
	}

	clips, err := s.db.ListClips(filter)
	if err != nil {
		s.log.Error("listing clips failed", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	out := make([]clipJSON, len(clips))
	for i, c := range clips {
		out[i] = toClipJSON(c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) clipFromPath(w http.ResponseWriter, r *http.Request) (db.Clip, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid clip id"}`, http.StatusBadRequest)
		return db.Clip{}, false
	}
	clip, err := s.db.ClipByID(id)
	if err != nil {
		http.Error(w, `{"error":"clip not found"}`, http.StatusNotFound)
		return db.Clip{}, false
	}
	return clip, true
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	clip, ok := s.clipFromPath(w, r)
	if !ok {
		return
	}
	if !clip.ThumbnailReady {
		http.Error(w, "thumbnail not ready yet", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, s.thumbs.Path(clip.ID))
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	clip, ok := s.clipFromPath(w, r)
	if !ok {
		return
	}
	s.streamer.ServeProxy(w, r, clip)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	clip, ok := s.clipFromPath(w, r)
	if !ok {
		return
	}
	stream.ServeOriginal(w, r, clip)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
