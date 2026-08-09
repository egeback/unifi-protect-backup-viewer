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
type SmartDetectEvent struct {
	ID       string // Protect's own event id, used to dedupe/upsert
	CameraID string
	Type     string
	// Detail (license plate text, matched face name) is always empty here:
	// the live websocket payload doesn't include the metadata it lives in,
	// unlike the legacy history API (see legacy.go). Only backfill fills it in.
	Detail  string
	Start   time.Time
	End     *time.Time
	RawJSON string
}

// wsEnvelope and wsItem match the confirmed live payload shape, e.g.:
//
//	{"type":"update","item":{"id":"...","modelKey":"event","type":"smartDetectZone",
//	 "start":1786280798761,"end":1786280805779,"device":"6a6f...046f",
//	 "smartDetectTypes":["face","person"]}}
//
// A single real-world detection produces several messages over its
// lifetime (an "add", then "update"s as classification refines and again
// once it ends) — all sharing the same item.id. The events table upserts
// on that id, so these collapse into one row reflecting the latest state.
type wsEnvelope struct {
	Type string `json:"type"` // "add" | "update"
	Item wsItem `json:"item"`
}

type wsItem struct {
	ID               string   `json:"id"`
	ModelKey         string   `json:"modelKey"`
	Type             string   `json:"type"`   // "smartDetectZone", "smartAudioDetect", ...
	Device           string   `json:"device"` // camera ID — NOT "camera"
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

// extractEvent returns ok=false for messages that aren't a classified
// smart-detect event (e.g. keepalives, camera state updates, or a
// freshly-added event that hasn't been classified yet — smartDetectTypes
// starts empty and fills in over the next update).
func extractEvent(msg []byte) (SmartDetectEvent, bool) {
	var env wsEnvelope
	if err := json.Unmarshal(msg, &env); err != nil {
		return SmartDetectEvent{}, false
	}
	item := env.Item

	if item.ModelKey != "event" || item.Device == "" || item.Start == 0 {
		return SmartDetectEvent{}, false
	}

	eventType := item.Type
	if len(item.SmartDetectTypes) > 0 {
		eventType = PickPrimaryType(item.SmartDetectTypes)
	}
	if eventType == "" {
		return SmartDetectEvent{}, false
	}

	result := SmartDetectEvent{
		ID:       item.ID,
		CameraID: item.Device,
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
