// Package store implements the persistent memory engine for Ohara.
// session_summarize.go contains the session summarization data-gathering logic.

package store

// SessionSummarizeResult holds context data about a session for summarization.
// The store layer gathers the raw data; the HTTP handler generates the summary
// text and populates GeneratedSummary.
type SessionSummarizeResult struct {
	ID               string   `json:"id"`
	Project          string   `json:"project"`
	StartedAt        string   `json:"started_at"`
	EndedAt          *string  `json:"ended_at,omitempty"`
	CurrentSummary   *string  `json:"current_summary,omitempty"`
	MemoryCount      int      `json:"memory_count"`
	PromptCount      int      `json:"prompt_count"`
	RecentPrompts    []string `json:"recent_prompts,omitempty"`
	GeneratedSummary *string  `json:"generated_summary,omitempty"`
}

// SessionSummarize gathers session metadata, memory count, prompt count, and
// recent prompt content for use in summarization. It is a pure data query —
// it does NOT generate or store a summary. The caller is responsible for
// producing the summary text and optionally persisting it via EndSession.
//
// If the session is not found, it returns ErrSessionNotFound.
// maxPrompts controls how many recent prompts are included (0 = none).
func (s *Store) SessionSummarize(id string, maxPrompts int) (*SessionSummarizeResult, error) {
	// Fetch session metadata.
	sess, err := s.GetSession(id)
	if err != nil {
		return nil, err
	}

	result := &SessionSummarizeResult{
		ID:             sess.ID,
		Project:        sess.Project,
		StartedAt:      sess.StartedAt,
		EndedAt:        sess.EndedAt,
		CurrentSummary: sess.Summary,
	}

	// Count active memory items associated with this session.
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM memory_items WHERE session_id = ? AND status = 'active'`,
		id,
	).Scan(&result.MemoryCount); err != nil {
		return nil, err
	}

	// Count total prompts for this session.
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM user_prompts WHERE session_id = ?`,
		id,
	).Scan(&result.PromptCount); err != nil {
		return nil, err
	}

	// Fetch recent prompt contents if requested.
	if maxPrompts > 0 {
		rows, err := s.queryItHook(s.db,
			`SELECT content FROM user_prompts
			 WHERE session_id = ?
			 ORDER BY created_at DESC LIMIT ?`,
			id, maxPrompts,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		// Collect in reverse so most-recent appears last in the slice.
		var prompts []string
		for rows.Next() {
			var content string
			if err := rows.Scan(&content); err != nil {
				return nil, err
			}
			prompts = append(prompts, content)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// Reverse to chronological order (oldest first).
		for i, j := 0, len(prompts)-1; i < j; i, j = i+1, j-1 {
			prompts[i], prompts[j] = prompts[j], prompts[i]
		}
		result.RecentPrompts = prompts
	}

	return result, nil
}
