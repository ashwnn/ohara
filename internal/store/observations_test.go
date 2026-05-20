package store

import (
	"strings"
	"testing"
)

func TestObserveSanitizesPrivateTagsAndTruncates(t *testing.T) {
	s := newTestStore(t)
	longBody := strings.Repeat("x", maxObservationBodyChars+200)
	longPayload := `{"secret":"<private>token-abc</private>", "blob":"` + strings.Repeat("y", maxObservationPayloadChars+400) + `"}`

	id, err := s.Observe(ObserveParams{
		SessionID:    "sess-obs-1",
		ProjectID:    "ohara",
		EventType:    "tool.execute.after",
		CaptureLevel: "full",
		Title:        "result <private>secret</private>",
		Body:         longBody,
		PayloadJSON:  longPayload,
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if id == 0 {
		t.Fatal("expected observation id")
	}

	var title, body, payload string
	if err := s.db.QueryRow(`SELECT title, body, payload_json FROM session_observations WHERE id = ?`, id).Scan(&title, &body, &payload); err != nil {
		t.Fatalf("read observation: %v", err)
	}
	if strings.Contains(strings.ToLower(title), "<private>") || strings.Contains(strings.ToLower(body), "<private>") || strings.Contains(strings.ToLower(payload), "<private>") {
		t.Fatal("private tags should be stripped from stored observation")
	}
	if len(body) > maxObservationBodyChars+30 {
		t.Fatalf("expected truncated body, len=%d", len(body))
	}
	if len(payload) > maxObservationPayloadChars+60 {
		t.Fatalf("expected truncated payload, len=%d", len(payload))
	}
}

func TestObserveNormalizesUnknownCaptureLevel(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Observe(ObserveParams{
		SessionID:    "sess-obs-2",
		ProjectID:    "ohara",
		EventType:    "session.status",
		CaptureLevel: "unknown-level",
		Title:        "status",
		Body:         "ok",
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}

	var level string
	if err := s.db.QueryRow(`SELECT capture_level FROM session_observations WHERE id = ?`, id).Scan(&level); err != nil {
		t.Fatalf("capture_level scan: %v", err)
	}
	if level != "metadata" {
		t.Fatalf("expected metadata fallback, got %q", level)
	}
}

func TestObservePreservesKnownCaptureLevels(t *testing.T) {
	s := newTestStore(t)
	levels := []string{"off", "prompts", "metadata", "tools", "full"}

	for i, level := range levels {
		id, err := s.Observe(ObserveParams{
			SessionID:    "sess-levels",
			ProjectID:    "ohara",
			EventType:    "session.updated",
			CaptureLevel: level,
			Title:        "level",
			Body:         "body",
		})
		if err != nil {
			t.Fatalf("Observe level %q: %v", level, err)
		}
		var got string
		if err := s.db.QueryRow(`SELECT capture_level FROM session_observations WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("scan capture_level for idx=%d: %v", i, err)
		}
		if got != level {
			t.Fatalf("expected capture_level %q, got %q", level, got)
		}
	}
}
