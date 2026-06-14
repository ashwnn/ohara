package store

import (
	"strings"
	"testing"
)

func TestGetMemoryOutcomesCount_NoOutcomes(t *testing.T) {
	s := newTestStore(t)
	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "outcomes-test",
		Kind:      MemoryKindProcedure,
		Title:     "Test proc",
		Body:      "Test body",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	success, failure, unknown, err := s.GetMemoryOutcomesCount(id)
	if err != nil {
		t.Fatalf("GetMemoryOutcomesCount: %v", err)
	}
	if success != 0 || failure != 0 || unknown != 0 {
		t.Fatalf("expected zero outcomes, got success=%d fail=%d unknown=%d", success, failure, unknown)
	}
}

func TestGetMemoryOutcomesCount_WithOutcomes(t *testing.T) {
	s := newTestStore(t)
	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "outcomes-test",
		Kind:      MemoryKindProcedure,
		Title:     "Test proc with outcomes",
		Body:      "Test body",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Add outcomes directly
	for _, st := range []string{"success", "success", "failure", "unknown"} {
		_, err := s.execHook(s.db,
			`INSERT INTO memory_outcomes (memory_id, status, actor_id) VALUES (?, ?, ?)`,
			id, st, "agent")
		if err != nil {
			t.Fatalf("insert outcome %s: %v", st, err)
		}
	}

	success, failure, unknown, err := s.GetMemoryOutcomesCount(id)
	if err != nil {
		t.Fatalf("GetMemoryOutcomesCount: %v", err)
	}
	if success != 2 {
		t.Errorf("expected 2 successes, got %d", success)
	}
	if failure != 1 {
		t.Errorf("expected 1 failure, got %d", failure)
	}
	if unknown != 1 {
		t.Errorf("expected 1 unknown, got %d", unknown)
	}
}

func TestGetMemoryOutcomesCountBatch(t *testing.T) {
	s := newTestStore(t)
	id1, _ := s.AddMemory(AddMemoryParams{
		ProjectID: "batch-test",
		Kind:      MemoryKindProcedure,
		Title:     "Proc 1",
		Body:      "Body 1",
	})
	id2, _ := s.AddMemory(AddMemoryParams{
		ProjectID: "batch-test",
		Kind:      MemoryKindProcedure,
		Title:     "Proc 2",
		Body:      "Body 2",
	})

	// Add outcomes for id1 only
	for _, st := range []string{"success", "failure"} {
		s.execHook(s.db,
			`INSERT INTO memory_outcomes (memory_id, status, actor_id) VALUES (?, ?, ?)`,
			id1, st, "agent")
	}

	result, err := s.GetMemoryOutcomesCountBatch([]int64{id1, id2})
	if err != nil {
		t.Fatalf("GetMemoryOutcomesCountBatch: %v", err)
	}
	if oc, ok := result[id1]; !ok {
		t.Fatal("expected id1 in batch result")
	} else if oc.Success != 1 || oc.Failure != 1 {
		t.Errorf("id1: expected 1 success, 1 failure; got %d success, %d failure", oc.Success, oc.Failure)
	}
	if _, ok := result[id2]; ok {
		t.Error("expected id2 not to be in batch result (no outcomes)")
	}
}

func TestFormatMemorySection_IncludesTriggerCondition(t *testing.T) {
	item := MemoryItem{
		ID:               1,
		Title:            "Handle timeout",
		Kind:             MemoryKindProcedure,
		Body:             "Retry with exponential backoff up to 3 attempts.",
		TriggerCondition: "When request times out after 30s",
		Tags:             []string{"network"},
		UtilityWeight:    0.0,
	}
	output := formatMemorySection(item)
	if !strings.Contains(output, "[when: When request times out after 30s]") {
		t.Errorf("expected trigger_condition in format output, got:\n%s", output)
	}
	if !strings.Contains(output, "network") {
		t.Errorf("expected tags in format output, got:\n%s", output)
	}
}

func TestFormatMemorySection_SkipsTriggerForNonProcedure(t *testing.T) {
	item := MemoryItem{
		ID:               2,
		Title:            "Auth decision",
		Kind:             MemoryKindDecision,
		Body:             "Use JWT.",
		TriggerCondition: "When user logs in",
	}
	output := formatMemorySection(item)
	if strings.Contains(output, "[when:") {
		t.Errorf("expected no trigger_condition for non-procedure, got:\n%s", output)
	}
}

func TestFormatMemorySection_IncludesUtilityWeight(t *testing.T) {
	item := MemoryItem{
		ID:            3,
		Title:         "Important pattern",
		Kind:          MemoryKindPattern,
		Body:          "Use middleware for auth.",
		UtilityWeight: 1.5,
	}
	output := formatMemorySection(item)
	if !strings.Contains(output, "[weight:") {
		t.Errorf("expected utility_weight in format output, got:\n%s", output)
	}
}

func TestFormatMemorySection_SkipsWeightWhenZero(t *testing.T) {
	item := MemoryItem{
		ID:            4,
		Title:         "Simple note",
		Kind:          MemoryKindDiscovery,
		Body:          "Just a note.",
		UtilityWeight: 0.0,
	}
	output := formatMemorySection(item)
	if strings.Contains(output, "[weight:") {
		t.Errorf("expected no weight for zero utility_weight, got:\n%s", output)
	}
}

func TestBuildPackOutput_EnrichedProcedureDisplay(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "pack-enrich",
		Kind:             MemoryKindProcedure,
		Title:            "Handle DB connection loss",
		Body:             "Reconnect with exponential backoff. Log the error. Alert if > 5 retries.",
		TriggerCondition: "When database connection drops",
		UtilityWeight:    2.0,
	})
	if err != nil {
		t.Fatalf("AddMemory procedure: %v", err)
	}

	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-enrich",
		BudgetTokens: 400,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}

	if result.Pack == "" {
		t.Fatal("expected non-empty pack")
	}
	if !strings.Contains(result.Pack, "When database connection drops") {
		t.Errorf("expected trigger_condition in pack output, got:\n%s", result.Pack)
	}
	if !strings.Contains(result.Pack, "exponential backoff") {
		t.Errorf("expected body content in pack output, got:\n%s", result.Pack)
	}
}

func TestBuildPackOutput_RichBodyWithTokenHint(t *testing.T) {
	s := newTestStore(t)

	// Create a memory with a long body to trigger the token hint
	longBody := strings.Repeat("This is a reasonably long body to verify token count hint appears in the output. ", 10)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "pack-enrich",
		Kind:      MemoryKindDecision,
		Title:     "Long body decision",
		Body:      longBody,
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-enrich",
		BudgetTokens: 600,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}

	// The body should appear in the pack output, with a token count hint
	if !strings.Contains(result.Pack, "tokens]") {
		t.Errorf("expected token count hint in pack output for long body, got:\n%s", result.Pack)
	}
}

func TestBuildPackOutput_ProcedureWithTriggerAndOutcomes(t *testing.T) {
	s := newTestStore(t)

	procID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "pack-outcomes",
		Kind:             MemoryKindProcedure,
		Title:            "Rate limit handler",
		Body:             "Apply rate limiting per user ID using sliding window.",
		TriggerCondition: "When user exceeds 100 requests per minute",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Add outcomes
	for _, st := range []string{"success", "success", "failure"} {
		s.execHook(s.db,
			`INSERT INTO memory_outcomes (memory_id, status, actor_id) VALUES (?, ?, ?)`,
			procID, st, "agent")
	}

	// Build pack with explain to confirm the item is included
	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-outcomes",
		BudgetTokens: 400,
		Explain:      true,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}

	// The pack text should include the procedure name and trigger_condition
	if !strings.Contains(result.Pack, "Rate limit handler") {
		t.Errorf("expected procedure title in pack, got:\n%s", result.Pack)
	}
	if !strings.Contains(result.Pack, "When user exceeds 100 requests per minute") {
		t.Errorf("expected trigger_condition in pack, got:\n%s", result.Pack)
	}

	// Verify the MemoryItems still have full body
	found := false
	for _, item := range result.MemoryItems {
		if item.ID == procID {
			found = true
			if !strings.Contains(item.Body, "sliding window") {
				t.Error("expected full body in MemoryItems")
			}
		}
	}
	if !found {
		t.Error("expected procedure in MemoryItems")
	}
}
