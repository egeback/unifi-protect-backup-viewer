// Package db owns the SQLite schema and query helpers for clips and events.
package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS clips (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	path            TEXT NOT NULL UNIQUE,
	day             TEXT NOT NULL, -- YYYY-MM-DD, the export folder's date
	camera_name     TEXT NOT NULL, -- raw (possibly mangled) name from filename
	camera_key      TEXT NOT NULL, -- normalized, for correlation
	start_ts        INTEGER NOT NULL, -- unix seconds
	end_ts          INTEGER NOT NULL,
	duration_s      INTEGER NOT NULL,
	size_bytes      INTEGER NOT NULL,
	mtime           INTEGER NOT NULL,
	event_type      TEXT NOT NULL DEFAULT 'unknown',
	event_source    TEXT NOT NULL DEFAULT 'unknown', -- protect | heuristic | unknown
	thumbnail_ready INTEGER NOT NULL DEFAULT 0,
	indexed_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_clips_day ON clips(day);
CREATE INDEX IF NOT EXISTS idx_clips_start_ts ON clips(start_ts);
CREATE INDEX IF NOT EXISTS idx_clips_camera_key ON clips(camera_key);
CREATE INDEX IF NOT EXISTS idx_clips_event_type ON clips(event_type);

CREATE TABLE IF NOT EXISTS events (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	camera_id    TEXT NOT NULL,
	camera_key   TEXT NOT NULL, -- normalized camera name, for correlation
	type         TEXT NOT NULL,
	start_ts     INTEGER NOT NULL,
	end_ts       INTEGER,
	raw_json     TEXT NOT NULL,
	received_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_camera_key ON events(camera_key);
CREATE INDEX IF NOT EXISTS idx_events_start_ts ON events(start_ts);
`

type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database at %s: %w", path, err)
	}
	// SQLite has no real concurrent-writer story; keep it simple and safe.
	sqlDB.SetMaxOpenConns(1)

	if _, err := sqlDB.Exec(schema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	return &DB{sqlDB}, nil
}

// Clip mirrors the clips table.
type Clip struct {
	ID             int64
	Path           string
	Day            string
	CameraName     string
	CameraKey      string
	Start          time.Time
	End            time.Time
	DurationS      int64
	SizeBytes      int64
	MTime          time.Time
	EventType      string
	EventSource    string
	ThumbnailReady bool
	IndexedAt      time.Time
}

// UpsertClip inserts a new clip or updates the mutable fields (size, mtime)
// of an existing one, identified by its filesystem path. It never
// overwrites an event_type that a later correlation pass has set unless the
// row is brand new.
func (d *DB) UpsertClip(c Clip) (int64, error) {
	res, err := d.Exec(`
		INSERT INTO clips (path, day, camera_name, camera_key, start_ts, end_ts, duration_s, size_bytes, mtime, event_type, event_source, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			size_bytes = excluded.size_bytes,
			mtime = excluded.mtime,
			indexed_at = excluded.indexed_at
	`, c.Path, c.Day, c.CameraName, c.CameraKey, c.Start.Unix(), c.End.Unix(), c.DurationS, c.SizeBytes, c.MTime.Unix(), c.EventType, c.EventSource, time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("upserting clip %s: %w", c.Path, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		// Row already existed (ON CONFLICT path): look it up.
		var existingID int64
		if err := d.QueryRow(`SELECT id FROM clips WHERE path = ?`, c.Path).Scan(&existingID); err != nil {
			return 0, fmt.Errorf("looking up id for %s: %w", c.Path, err)
		}
		return existingID, nil
	}
	return id, nil
}

// UnclassifiedClips returns clips whose event_type still needs correlation
// against real Protect events (event_source = 'unknown').
func (d *DB) UnclassifiedClips() ([]Clip, error) {
	rows, err := d.Query(`SELECT id, path, day, camera_name, camera_key, start_ts, end_ts, duration_s FROM clips WHERE event_source = 'unknown'`)
	if err != nil {
		return nil, fmt.Errorf("querying unclassified clips: %w", err)
	}
	defer rows.Close()

	var clips []Clip
	for rows.Next() {
		var c Clip
		var startTs, endTs int64
		if err := rows.Scan(&c.ID, &c.Path, &c.Day, &c.CameraName, &c.CameraKey, &startTs, &endTs, &c.DurationS); err != nil {
			return nil, fmt.Errorf("scanning clip row: %w", err)
		}
		c.Start = time.Unix(startTs, 0)
		c.End = time.Unix(endTs, 0)
		clips = append(clips, c)
	}
	return clips, rows.Err()
}

// NonProtectClips returns every clip that doesn't yet have a real Protect
// event match — both event_source = 'unknown' (not yet processed at all)
// and 'heuristic' (already given a best-effort guess, but still eligible
// to be upgraded if a real historical event turns up via backfill).
func (d *DB) NonProtectClips() ([]Clip, error) {
	rows, err := d.Query(`SELECT id, path, day, camera_name, camera_key, start_ts, end_ts, duration_s FROM clips WHERE event_source != 'protect'`)
	if err != nil {
		return nil, fmt.Errorf("querying non-protect clips: %w", err)
	}
	defer rows.Close()

	var clips []Clip
	for rows.Next() {
		var c Clip
		var startTs, endTs int64
		if err := rows.Scan(&c.ID, &c.Path, &c.Day, &c.CameraName, &c.CameraKey, &startTs, &endTs, &c.DurationS); err != nil {
			return nil, fmt.Errorf("scanning clip row: %w", err)
		}
		c.Start = time.Unix(startTs, 0)
		c.End = time.Unix(endTs, 0)
		clips = append(clips, c)
	}
	return clips, rows.Err()
}

// EarliestNonProtectClipStart returns the start time of the oldest clip
// that still isn't protect-sourced, i.e. how far back a backfill needs to
// look. ok is false if every clip already has a real Protect match.
func (d *DB) EarliestNonProtectClipStart() (t time.Time, ok bool, err error) {
	var startTs *int64
	err = d.QueryRow(`SELECT MIN(start_ts) FROM clips WHERE event_source != 'protect'`).Scan(&startTs)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("finding earliest non-protect clip: %w", err)
	}
	if startTs == nil {
		return time.Time{}, false, nil
	}
	return time.Unix(*startTs, 0), true, nil
}

// SetClipClassification updates a clip's event_type/event_source after correlation.
func (d *DB) SetClipClassification(id int64, eventType, eventSource string) error {
	_, err := d.Exec(`UPDATE clips SET event_type = ?, event_source = ? WHERE id = ?`, eventType, eventSource, id)
	if err != nil {
		return fmt.Errorf("updating classification for clip %d: %w", id, err)
	}
	return nil
}

// MarkThumbnailReady flags a clip as having a generated thumbnail.
func (d *DB) MarkThumbnailReady(id int64) error {
	_, err := d.Exec(`UPDATE clips SET thumbnail_ready = 1 WHERE id = ?`, id)
	return err
}

// ClipsWithoutThumbnail returns clip IDs and paths still missing a
// thumbnail, newest first — newest is what the UI shows by default, and on
// a large backlog (e.g. after a fresh reindex) there's no reason to make
// today's clips wait behind months-old ones.
func (d *DB) ClipsWithoutThumbnail(limit int) ([]Clip, error) {
	rows, err := d.Query(`SELECT id, path, start_ts, end_ts FROM clips WHERE thumbnail_ready = 0 ORDER BY start_ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying clips without thumbnail: %w", err)
	}
	defer rows.Close()

	var clips []Clip
	for rows.Next() {
		var c Clip
		var startTs, endTs int64
		if err := rows.Scan(&c.ID, &c.Path, &startTs, &endTs); err != nil {
			return nil, fmt.Errorf("scanning clip row: %w", err)
		}
		c.Start = time.Unix(startTs, 0)
		c.End = time.Unix(endTs, 0)
		clips = append(clips, c)
	}
	return clips, rows.Err()
}

// ClipByID fetches a single clip.
func (d *DB) ClipByID(id int64) (Clip, error) {
	var c Clip
	var startTs, endTs, mtime, indexedAt int64
	var thumbReady int
	err := d.QueryRow(`SELECT id, path, day, camera_name, camera_key, start_ts, end_ts, duration_s, size_bytes, mtime, event_type, event_source, thumbnail_ready, indexed_at FROM clips WHERE id = ?`, id).
		Scan(&c.ID, &c.Path, &c.Day, &c.CameraName, &c.CameraKey, &startTs, &endTs, &c.DurationS, &c.SizeBytes, &mtime, &c.EventType, &c.EventSource, &thumbReady, &indexedAt)
	if err != nil {
		return Clip{}, fmt.Errorf("fetching clip %d: %w", id, err)
	}
	c.Start = time.Unix(startTs, 0)
	c.End = time.Unix(endTs, 0)
	c.MTime = time.Unix(mtime, 0)
	c.IndexedAt = time.Unix(indexedAt, 0)
	c.ThumbnailReady = thumbReady == 1
	return c, nil
}

// ClipFilter narrows ListClips results.
type ClipFilter struct {
	Day       string // YYYY-MM-DD, empty = all
	CameraKey string // empty = all
	EventType string // empty = all
	Limit     int
	Offset    int
}

// ListClips returns clips matching the filter, newest first.
func (d *DB) ListClips(f ClipFilter) ([]Clip, error) {
	query := `SELECT id, path, day, camera_name, camera_key, start_ts, end_ts, duration_s, size_bytes, event_type, event_source, thumbnail_ready FROM clips WHERE 1=1`
	var args []any
	if f.Day != "" {
		query += ` AND day = ?`
		args = append(args, f.Day)
	}
	if f.CameraKey != "" {
		query += ` AND camera_key = ?`
		args = append(args, f.CameraKey)
	}
	if f.EventType != "" {
		query += ` AND event_type = ?`
		args = append(args, f.EventType)
	}
	query += ` ORDER BY start_ts DESC`
	if f.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing clips: %w", err)
	}
	defer rows.Close()

	var clips []Clip
	for rows.Next() {
		var c Clip
		var startTs, endTs int64
		var thumbReady int
		if err := rows.Scan(&c.ID, &c.Path, &c.Day, &c.CameraName, &c.CameraKey, &startTs, &endTs, &c.DurationS, &c.SizeBytes, &c.EventType, &c.EventSource, &thumbReady); err != nil {
			return nil, fmt.Errorf("scanning clip row: %w", err)
		}
		c.Start = time.Unix(startTs, 0)
		c.End = time.Unix(endTs, 0)
		c.ThumbnailReady = thumbReady == 1
		clips = append(clips, c)
	}
	return clips, rows.Err()
}

// Days returns the distinct list of days (YYYY-MM-DD) that have clips, newest first.
func (d *DB) Days() ([]string, error) {
	rows, err := d.Query(`SELECT DISTINCT day FROM clips ORDER BY day DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing days: %w", err)
	}
	defer rows.Close()

	var days []string
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}

// Cameras returns the distinct cameras seen in clips (key + a display name).
func (d *DB) Cameras() ([]Camera, error) {
	rows, err := d.Query(`SELECT camera_key, MAX(camera_name), COUNT(*) FROM clips GROUP BY camera_key ORDER BY MAX(camera_name)`)
	if err != nil {
		return nil, fmt.Errorf("listing cameras: %w", err)
	}
	defer rows.Close()

	var cams []Camera
	for rows.Next() {
		var c Camera
		if err := rows.Scan(&c.Key, &c.Name, &c.ClipCount); err != nil {
			return nil, err
		}
		cams = append(cams, c)
	}
	return cams, rows.Err()
}

type Camera struct {
	Key       string
	Name      string
	ClipCount int
}

// InsertEvent stores a Protect smart-detect event for later correlation.
func (d *DB) InsertEvent(cameraID, cameraKey, eventType string, start time.Time, end *time.Time, rawJSON string) error {
	var endTs any
	if end != nil {
		endTs = end.Unix()
	}
	_, err := d.Exec(`INSERT INTO events (camera_id, camera_key, type, start_ts, end_ts, raw_json, received_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cameraID, cameraKey, eventType, start.Unix(), endTs, rawJSON, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("inserting event: %w", err)
	}
	return nil
}

// FindOverlappingEvent returns the smart-detect event type for a camera
// whose time window overlaps [start,end] (with a small tolerance), if any.
func (d *DB) FindOverlappingEvent(cameraKey string, start, end time.Time, tolerance time.Duration) (eventType string, found bool, err error) {
	lo := start.Add(-tolerance).Unix()
	hi := end.Add(tolerance).Unix()
	row := d.QueryRow(`
		SELECT type FROM events
		WHERE camera_key = ?
		  AND start_ts <= ?
		  AND (end_ts IS NULL OR end_ts >= ?)
		ORDER BY start_ts DESC
		LIMIT 1
	`, cameraKey, hi, lo)
	var t string
	err = row.Scan(&t)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("finding overlapping event: %w", err)
	}
	return t, true, nil
}

// PruneOldEvents deletes events older than the given cutoff, keeping the table small.
func (d *DB) PruneOldEvents(cutoff time.Time) (int64, error) {
	res, err := d.Exec(`DELETE FROM events WHERE start_ts < ?`, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("pruning old events: %w", err)
	}
	return res.RowsAffected()
}
