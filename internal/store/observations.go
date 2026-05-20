package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ashwnn/ohara/internal/redact"
	"github.com/ashwnn/ohara/internal/util"
)

const (
	maxObservationTitleChars   = 240
	maxObservationBodyChars    = 8000
	maxObservationPayloadChars = 12000
)

// ObserveParams captures raw, session-scoped events from plugin adapters.
// Observations are not authoritative memories and are stored in a separate lane.
type ObserveParams struct {
	SessionID    string `json:"session_id"`
	ProjectID    string `json:"project_id"`
	EventType    string `json:"event_type"`
	CaptureLevel string `json:"capture_level,omitempty"` // off|prompts|metadata|tools|full
	Source       string `json:"source,omitempty"`        // plugin/runtime source
	Title        string `json:"title,omitempty"`         // short event summary
	Body         string `json:"body,omitempty"`          // truncated human text
	PayloadJSON  string `json:"payload_json,omitempty"`  // raw structured payload
}

func sanitizeObservationText(value string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	normalized := strings.TrimSpace(stripPrivateTags(redact.Redact(value)))
	return util.Truncate(normalized, maxChars)
}

// Observe stores a raw session observation for later enrichment/consolidation.
func (s *Store) Observe(p ObserveParams) (int64, error) {
	if strings.TrimSpace(p.SessionID) == "" {
		return 0, fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(p.ProjectID) == "" {
		return 0, fmt.Errorf("project_id is required")
	}
	if strings.TrimSpace(p.EventType) == "" {
		return 0, fmt.Errorf("event_type is required")
	}

	projectID, _ := NormalizeProject(p.ProjectID)
	title := sanitizeObservationText(p.Title, maxObservationTitleChars)
	body := sanitizeObservationText(p.Body, maxObservationBodyChars)
	payloadJSON := sanitizeObservationText(p.PayloadJSON, maxObservationPayloadChars)
	if payloadJSON == "" {
		payloadJSON = "{}"
	} else if !json.Valid([]byte(payloadJSON)) {
		fallback, _ := json.Marshal(map[string]string{"raw": payloadJSON})
		payloadJSON = string(fallback)
	}

	level := strings.ToLower(strings.TrimSpace(p.CaptureLevel))
	switch level {
	case "off", "prompts", "metadata", "tools", "full":
		// valid
	default:
		level = "metadata"
	}

	source := strings.TrimSpace(p.Source)
	if source == "" {
		source = "opencode"
	}

	res, err := s.execHook(s.db,
		`INSERT INTO session_observations (
			session_id, project_id, event_type, capture_level, source, title, body, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.SessionID, projectID, strings.TrimSpace(p.EventType), level, source, title, body, payloadJSON,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}
