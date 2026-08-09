package protect

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// SmartDetectEvent is our normalized view of a Protect event message.
// The exact WebSocket payload shape is NOT fully verified against a live
// event as of writing (the confirmed endpoint stayed open for 90s without
// producing a message during testing — there was no motion to trigger one).
// RawJSON is always preserved so nothing is lost if these guessed field
// names turn out to need adjusting once real traffic is observed; check
// the "raw event received" debug log line after a real motion event to
// confirm/fix the field mapping.
type SmartDetectEvent struct {
	CameraID string
	Type     string
	Start    time.Time
	End      *time.Time
	RawJSON  string
}

// wsEnvelope covers the couple of plausible shapes Protect's Integration
// API might wrap events in ("type"/"item" action-style, or a bare event).
type wsEnvelope struct {
	Action string          `json:"action"`
	Type   string          `json:"type"`
	Item   json.RawMessage `json:"item"`
	// Fallback: some messages may be the event itself, unwrapped.
	ID               string   `json:"id"`
	Camera           string   `json:"camera"`
	EventType        string   `json:"type_"` // placeholder, overwritten by best match below
	SmartDetectTypes []string `json:"smartDetectTypes"`
	Start            int64    `json:"start"`
	End              *int64   `json:"end"`
}

type wsItem struct {
	ModelKey         string   `json:"modelKey"`
	ID               string   `json:"id"`
	Camera           string   `json:"camera"`
	Type             string   `json:"type"`
	SmartDetectTypes []string `json:"smartDetectTypes"`
	Start            int64    `json:"start"`
	End              *int64   `json:"end"`
}

// Listen connects to the Protect event WebSocket and calls onEvent for each
// message it can extract a usable event from. It reconnects with backoff on
// any disconnect and runs until ctx is cancelled.
func Listen(ctx context.Context, c *Client, log *slog.Logger, onEvent func(SmartDetectEvent)) {
	backoff := time.Second
	const maxBackoff = time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := connectAndRead(ctx, c, log, onEvent); err != nil {
			log.Warn("protect event websocket disconnected", "error", err, "retry_in", backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func connectAndRead(ctx context.Context, c *Client, log *slog.Logger, onEvent func(SmartDetectEvent)) error {
	dialer := websocket.Dialer{
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
		HandshakeTimeout: 10 * time.Second,
	}
	header := http.Header{}
	header.Set("X-API-KEY", c.apiKey)

	conn, _, err := dialer.DialContext(ctx, c.WebSocketURL(), header)
	if err != nil {
		return err
	}
	defer conn.Close()

	log.Info("connected to protect event websocket")
	backoffReset := make(chan struct{}, 1)
	backoffReset <- struct{}{}

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		log.Debug("raw event received", "payload", string(msg))

		ev, ok := extractEvent(msg)
		if !ok {
			continue
		}
		onEvent(ev)
	}
}

// extractEvent tries the couple of plausible payload shapes. Returns
// ok=false for messages that aren't a smart-detect-style event (e.g.
// keepalives, camera state updates).
func extractEvent(msg []byte) (SmartDetectEvent, bool) {
	var env wsEnvelope
	if err := json.Unmarshal(msg, &env); err != nil {
		return SmartDetectEvent{}, false
	}

	var item wsItem
	if len(env.Item) > 0 {
		if err := json.Unmarshal(env.Item, &item); err != nil {
			return SmartDetectEvent{}, false
		}
	} else {
		item = wsItem{
			ID:               env.ID,
			Camera:           env.Camera,
			SmartDetectTypes: env.SmartDetectTypes,
			Start:            env.Start,
			End:              env.End,
		}
	}

	if item.Camera == "" || item.Start == 0 {
		return SmartDetectEvent{}, false
	}

	eventType := "motion"
	if len(item.SmartDetectTypes) > 0 {
		eventType = item.SmartDetectTypes[0]
	} else if item.Type != "" {
		eventType = item.Type
	}

	result := SmartDetectEvent{
		CameraID: item.Camera,
		Type:     eventType,
		Start:    time.UnixMilli(item.Start),
		RawJSON:  string(msg),
	}
	if item.End != nil {
		t := time.UnixMilli(*item.End)
		result.End = &t
	}
	return result, true
}
