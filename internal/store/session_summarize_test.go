package store

import (
	"testing"
)

func TestSessionSummarize_ReturnsSessionData(t *testing.T) {
	s := newTestStore(t)

	// Create a session.
	if err := s.CreateSession("summ-sess-1", "ohara", "/tmp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Add some memories for the session.
	for i := 0; i < 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID: "ohara",
			Kind:      MemoryKindDecision,
			Scope:     MemoryScopeProject,
			Title:     "test decision",
			Body:      "test body",
			SessionID: "summ-sess-1",
		})
		if err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}
	}

	// Add some prompts for the session.
	for i := 0; i < 2; i++ {
		_, err := s.AddPrompt(AddPromptParams{
			SessionID: "summ-sess-1",
			Content:   "prompt content",
			Project:   "ohara",
		})
		if err != nil {
			t.Fatalf("AddPrompt %d: %v", i, err)
		}
	}

	result, err := s.SessionSummarize("summ-sess-1", 5)
	if err != nil {
		t.Fatalf("SessionSummarize: %v", err)
	}

	if result.ID != "summ-sess-1" {
		t.Errorf("expected ID 'summ-sess-1', got %q", result.ID)
	}
	if result.Project != "ohara" {
		t.Errorf("expected project 'ohara', got %q", result.Project)
	}
	if result.StartedAt == "" {
		t.Error("expected non-empty started_at")
	}
	if result.EndedAt != nil {
		t.Error("expected ended_at to be nil (session not ended)")
	}
	if result.CurrentSummary != nil {
		t.Error("expected current_summary to be nil (no summary set)")
	}
	if result.MemoryCount != 3 {
		t.Errorf("expected 3 memories, got %d", result.MemoryCount)
	}
	if result.PromptCount != 2 {
		t.Errorf("expected 2 prompts, got %d", result.PromptCount)
	}
	if len(result.RecentPrompts) != 2 {
		t.Errorf("expected 2 recent prompts, got %d", len(result.RecentPrompts))
	}
}

func TestSessionSummarize_NoPrompts(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("summ-noprompts", "ohara", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	result, err := s.SessionSummarize("summ-noprompts", 5)
	if err != nil {
		t.Fatalf("SessionSummarize: %v", err)
	}

	if result.MemoryCount != 0 {
		t.Errorf("expected 0 memories, got %d", result.MemoryCount)
	}
	if result.PromptCount != 0 {
		t.Errorf("expected 0 prompts, got %d", result.PromptCount)
	}
	if len(result.RecentPrompts) != 0 {
		t.Errorf("expected 0 recent prompts, got %d", len(result.RecentPrompts))
	}
}

func TestSessionSummarize_ZeroMaxPromptsSkipsPrompts(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("summ-zeromax", "ohara", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err := s.AddPrompt(AddPromptParams{
		SessionID: "summ-zeromax",
		Content:   "hello",
	})
	if err != nil {
		t.Fatalf("AddPrompt: %v", err)
	}

	result, err := s.SessionSummarize("summ-zeromax", 0)
	if err != nil {
		t.Fatalf("SessionSummarize: %v", err)
	}

	if result.PromptCount != 1 {
		t.Errorf("expected 1 prompt (still counted), got %d", result.PromptCount)
	}
	if len(result.RecentPrompts) != 0 {
		t.Errorf("expected 0 recent prompts when maxPrompts=0, got %d", len(result.RecentPrompts))
	}
}

func TestSessionSummarize_EndedSessionIncludesEndedAt(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("summ-ended", "ohara", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.EndSession("summ-ended", "my summary"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	result, err := s.SessionSummarize("summ-ended", 0)
	if err != nil {
		t.Fatalf("SessionSummarize: %v", err)
	}

	if result.EndedAt == nil {
		t.Error("expected ended_at to be non-nil for ended session")
	}
	if result.CurrentSummary == nil {
		t.Error("expected current_summary to be non-nil for ended session")
	}
	if result.CurrentSummary != nil && *result.CurrentSummary != "my summary" {
		t.Errorf("expected summary 'my summary', got %q", *result.CurrentSummary)
	}
}

func TestSessionSummarize_NotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.SessionSummarize("does-not-exist", 5)
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}

func TestSessionSummarize_PromptsInChronologicalOrder(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("summ-order", "ohara", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Add prompts in order.
	for i := 0; i < 3; i++ {
		_, err := s.AddPrompt(AddPromptParams{
			SessionID: "summ-order",
			Content:   "prompt",
		})
		if err != nil {
			t.Fatalf("AddPrompt %d: %v", i, err)
		}
	}

	result, err := s.SessionSummarize("summ-order", 10)
	if err != nil {
		t.Fatalf("SessionSummarize: %v", err)
	}

	if len(result.RecentPrompts) != 3 {
		t.Fatalf("expected 3 prompts, got %d", len(result.RecentPrompts))
	}
}

func TestSessionSummarize_MaxPromptsLimit(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("summ-limit", "ohara", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, err := s.AddPrompt(AddPromptParams{
			SessionID: "summ-limit",
			Content:   "prompt",
		})
		if err != nil {
			t.Fatalf("AddPrompt %d: %v", i, err)
		}
	}

	// Ask for only 2 most recent prompts.
	result, err := s.SessionSummarize("summ-limit", 2)
	if err != nil {
		t.Fatalf("SessionSummarize: %v", err)
	}

	if len(result.RecentPrompts) != 2 {
		t.Errorf("expected 2 recent prompts (limited by maxPrompts), got %d", len(result.RecentPrompts))
	}
}
