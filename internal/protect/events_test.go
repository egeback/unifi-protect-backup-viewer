package protect

import "testing"

// Fixtures captured from a live wss://.../subscribe/events connection on
// 2026-08-09 (face/person detections on a real camera).
const (
	rawFaceUpdate = `{"type":"update","item":{"id":"5e1a9d98-b56c-4274-8b51-caa3afdb6e73","modelKey":"event","type":"smartDetectZone","start":1786280798761,"device":"6a6f3f640025c603e400046f","smartDetectTypes":["face"]}}`

	rawFaceEnded = `{"type":"update","item":{"id":"5e1a9d98-b56c-4274-8b51-caa3afdb6e73","modelKey":"event","type":"smartDetectZone","start":1786280798761,"end":1786280805779,"device":"6a6f3f640025c603e400046f","smartDetectTypes":["face","person"]}}`

	rawAudioNoTypeYet = `{"item":{"id":"6a787baf03b6c603e4050150","modelKey":"event","type":"smartAudioDetect","start":1786280865907,"device":"6a7851080074c603e404cd86","smartDetectTypes":[]},"type":"add"}`

	rawAudioClassified = `{"item":{"id":"6a787baf03b6c603e4050150","modelKey":"event","type":"smartAudioDetect","start":1786280865907,"device":"6a7851080074c603e404cd86","smartDetectTypes":["alrmSpeak"]},"type":"update"}`
)

func TestExtractEvent(t *testing.T) {
	ev, ok := extractEvent([]byte(rawFaceUpdate))
	if !ok {
		t.Fatal("expected ok=true for a classified smartDetectZone event")
	}
	if ev.CameraID != "6a6f3f640025c603e400046f" {
		t.Errorf("CameraID = %q, want the device field's value", ev.CameraID)
	}
	if ev.Type != "face" {
		t.Errorf("Type = %q, want %q", ev.Type, "face")
	}
	if ev.End != nil {
		t.Errorf("End = %v, want nil (event hasn't ended yet)", ev.End)
	}

	ev, ok = extractEvent([]byte(rawFaceEnded))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ev.End == nil {
		t.Fatal("expected End to be set once the event has an end timestamp")
	}

	ev, ok = extractEvent([]byte(rawAudioClassified))
	if !ok {
		t.Fatal("expected ok=true for a classified smartAudioDetect event")
	}
	if ev.Type != "alrmSpeak" {
		t.Errorf("Type = %q, want %q", ev.Type, "alrmSpeak")
	}
}

// TestExtractEventFallsBackToItemType covers the "add" stage of an event,
// before smartDetectTypes has been classified yet — we still get a usable
// (if less specific) type from item.type itself, rather than dropping the
// message. A later "update" with the real classification just becomes a
// second, more specific row for the same time window.
func TestExtractEventFallsBackToItemType(t *testing.T) {
	ev, ok := extractEvent([]byte(rawAudioNoTypeYet))
	if !ok {
		t.Fatal("expected ok=true, falling back to item.type when smartDetectTypes is empty")
	}
	if ev.Type != "smartAudioDetect" {
		t.Errorf("Type = %q, want %q", ev.Type, "smartAudioDetect")
	}
}
