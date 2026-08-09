package protect

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"
)

// LegacyClient authenticates against the same session-cookie API the
// Protect web UI itself uses (not the API-key Integration API). It exists
// solely because the Integration API has no historical event-search
// endpoint (confirmed: GET /integration/v1/events -> 404) while this one
// does — /proxy/protect/api/events?start=...&end=... returns real
// historical smart-detect events, going back at least as far as Protect's
// own retention.
type LegacyClient struct {
	host       string
	username   string
	password   string
	httpClient *http.Client

	mu     sync.Mutex
	csrf   string
	expiry time.Time
}

func NewLegacyClient(host, username, password string) *LegacyClient {
	jar, _ := cookiejar.New(nil)
	return &LegacyClient{
		host:     host,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

func (c *LegacyClient) ensureLoggedIn(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expiry) {
		return nil
	}

	body, err := json.Marshal(map[string]string{"username": c.username, "password": c.password})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://%s/api/auth/login", c.host), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: %s", resp.Status)
	}

	csrf := resp.Header.Get("X-Updated-Csrf-Token")
	if csrf == "" {
		csrf = resp.Header.Get("X-Csrf-Token")
	}
	if csrf == "" {
		return fmt.Errorf("login succeeded but no CSRF token in response headers")
	}
	c.csrf = csrf
	// The session JWT is valid ~2h; refresh a bit early rather than race it.
	c.expiry = time.Now().Add(90 * time.Minute)
	return nil
}

// LegacyEvent is a historical smart-detect event as returned by the legacy
// events API. Only classified, camera-attributed events are returned by
// Events() — system/access events (camera == nil) and not-yet-classified
// ones (empty SmartDetectTypes) are filtered out.
type LegacyEvent struct {
	ID          string
	PrimaryType string // best single classification, via PickPrimaryType
	// Detail is a license plate reading or a recognized face's name, when
	// Protect's own object/face recognition matched one confidently enough
	// — empty otherwise (e.g. no "Known Faces" configured, or no match).
	// Unlike the live websocket, this API's response includes the metadata
	// this comes from.
	Detail string
	Camera string
	Start  int64 // unix ms
	End    *int64
}

// detectedThumbnail mirrors the parts of a legacy event's
// metadata.detectedThumbnails entries we care about. Best-effort: this is
// an undocumented internal shape, so any field that's missing or not what
// we expect just yields an empty Detail rather than an error.
type detectedThumbnail struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Attributes struct {
		MatchedName string `json:"matchedName"`
	} `json:"attributes"`
}

// pickDetail finds the license plate reading or matched face name
// associated with the chosen primary type, if any. A license plate reading
// lives inside the "vehicle" thumbnail entry (labeled with
// "smartDetectType:licensePlate"), not a separate "licensePlate" one.
func pickDetail(primaryType string, thumbnails []detectedThumbnail) string {
	var wantType string
	switch primaryType {
	case "licensePlate", "vehicle":
		wantType = "vehicle"
	case "face":
		wantType = "face"
	default:
		return ""
	}
	for _, th := range thumbnails {
		if th.Type != wantType {
			continue
		}
		if th.Name != "" {
			return th.Name
		}
		if th.Attributes.MatchedName != "" {
			return th.Attributes.MatchedName
		}
	}
	return ""
}

// Events fetches historical events in [start, end]. The API has shown no
// sign of pagination at the volumes this app deals with (a home NVR's
// weekly event count); if that ever changes, this is the place to add it.
func (c *LegacyClient) Events(ctx context.Context, start, end time.Time) ([]LegacyEvent, error) {
	if err := c.ensureLoggedIn(ctx); err != nil {
		return nil, fmt.Errorf("logging in: %w", err)
	}

	url := fmt.Sprintf("https://%s/proxy/protect/api/events?start=%d&end=%d", c.host, start.UnixMilli(), end.UnixMilli())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	req.Header.Set("X-CSRF-Token", c.csrf)
	c.mu.Unlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("events request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// Session may have been invalidated server-side; force a fresh
		// login on the next call rather than keep failing until expiry.
		c.mu.Lock()
		c.expiry = time.Time{}
		c.mu.Unlock()
		return nil, fmt.Errorf("unauthorized (session invalidated?)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var raw []struct {
		ID               string   `json:"id"`
		Camera           *string  `json:"camera"`
		SmartDetectTypes []string `json:"smartDetectTypes"`
		Start            int64    `json:"start"`
		End              *int64   `json:"end"`
		Metadata         struct {
			DetectedThumbnails []detectedThumbnail `json:"detectedThumbnails"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding events response: %w", err)
	}

	events := make([]LegacyEvent, 0, len(raw))
	for _, r := range raw {
		if r.Camera == nil || *r.Camera == "" || len(r.SmartDetectTypes) == 0 {
			continue // system/access events, or not yet classified
		}
		primary := PickPrimaryType(r.SmartDetectTypes)
		events = append(events, LegacyEvent{
			ID:          r.ID,
			PrimaryType: primary,
			Detail:      pickDetail(primary, r.Metadata.DetectedThumbnails),
			Camera:      *r.Camera,
			Start:       r.Start,
			End:         r.End,
		})
	}
	return events, nil
}
