package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFileHistoryMatchesAppliesToAndBodyFallback(t *testing.T) {
	s := newTestStore(t)
	path := "services/auth/middleware.go"

	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID:     "ohara",
		Kind:          MemoryKindBugfix,
		Title:         "Fixed nil pointer in auth middleware",
		Body:          "Handled nil claims in auth middleware",
		AppliesToJSON: `{"files":["services/auth/middleware.go"]}`,
	})
	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth flow update",
		Body:      "This applies to services/auth/middleware.go and token policy.",
	})

	items, err := s.FileHistory(path, "ohara", 10)
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least 2 file-history items, got %d", len(items))
	}
}

func TestFileHistoryPrefersStructuredFieldsAndSupportsRenameHints(t *testing.T) {
	s := newTestStore(t)
	targetPath := "internal/auth/middleware.go"

	relatedJSON, _ := json.Marshal(map[string]any{
		"renamed_from": "services/auth/middleware.go",
		"renamed_to":   "internal/auth/middleware.go",
	})
	evidenceJSON, _ := json.Marshal(map[string]any{
		"file": "internal/auth/middleware.go",
	})

	structuredID, _ := s.AddMemory(AddMemoryParams{
		ProjectID:      "ohara",
		Kind:           MemoryKindDecision,
		Title:          "Middleware auth decision",
		Body:           "Use strict audience checks.",
		AppliesToJSON:  `{"files":["internal/auth/middleware.go"]}`,
		EvidenceJSON:   string(evidenceJSON),
		RelatedJSON:    string(relatedJSON),
		Classification: "foundational",
	})
	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Mentioned middleware in notes",
		Body:      "something about middleware",
	})

	items, err := s.FileHistory(targetPath, "ohara", 5)
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected file history results")
	}
	if items[0].ID != structuredID {
		t.Fatalf("expected structured match first, got id=%d", items[0].ID)
	}

	renamedItems, err := s.FileHistory("services/auth/middleware.go", "ohara", 5)
	if err != nil {
		t.Fatalf("FileHistory renamed path: %v", err)
	}
	if len(renamedItems) == 0 {
		t.Fatal("expected rename-hint match for old path")
	}
}

func TestFileHistoryRespectsProjectScopeAndExcludesExpired(t *testing.T) {
	s := newTestStore(t)
	path := "internal/auth/middleware.go"
	id, _ := s.AddMemory(AddMemoryParams{
		ProjectID:     "ohara",
		Kind:          MemoryKindBugfix,
		Title:         "Auth middleware fix",
		Body:          "Fix auth middleware path bug",
		AppliesToJSON: `{"files":["internal/auth/middleware.go"]}`,
	})
	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID:     "other-project",
		Kind:          MemoryKindBugfix,
		Title:         "Other project middleware fix",
		Body:          "Should not leak",
		AppliesToJSON: `{"files":["internal/auth/middleware.go"]}`,
	})
	if _, err := s.Exec(`UPDATE memory_items SET expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`, id); err != nil {
		t.Fatalf("expire memory: %v", err)
	}

	items, err := s.FileHistory(path, "ohara", 10)
	if err != nil {
		t.Fatalf("FileHistory scoped: %v", err)
	}
	for _, item := range items {
		if item.ProjectID != "ohara" {
			t.Fatalf("wrong project leak: %s", item.ProjectID)
		}
		if item.ID == id {
			t.Fatal("expired memory should not appear in file history")
		}
	}
}

func TestFileContextBuildsBudgetedContext(t *testing.T) {
	s := newTestStore(t)
	path := "services/auth/middleware.go"

	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID:     "ohara",
		Kind:          MemoryKindBugfix,
		Title:         "Fixed auth panic",
		Body:          "Guard nil principal before checking roles.",
		AppliesToJSON: `{"files":["services/auth/middleware.go"]}`,
	})
	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID:      "ohara",
		Kind:           MemoryKindProcedure,
		Title:          "Auth incident playbook",
		Body:           "When auth fails, inspect token parser and middleware sequence.",
		AppliesToJSON:  `{"files":["services/auth/middleware.go"]}`,
		Classification: "foundational",
	})

	result, err := s.FileContext(path, "ohara", 180)
	if err != nil {
		t.Fatalf("FileContext: %v", err)
	}
	if result.ItemCount == 0 || result.Context == "" {
		t.Fatal("expected non-empty file context")
	}
	if result.TokenCount > 180 {
		t.Fatalf("expected token count <= 180, got %d", result.TokenCount)
	}
	if !strings.Contains(result.Context, "File Context: "+path) {
		t.Fatalf("expected context header to include path, got: %q", result.Context)
	}
}

func TestFileHistoryDirectoryLevelMatch(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID:     "ohara",
		Kind:          MemoryKindProcedure,
		Title:         "Router migration procedure",
		Body:          "Validate middleware sequence when editing router.",
		AppliesToJSON: `{"files":["internal/http/"]}`,
	})

	items, err := s.FileHistory("internal/http/router.go", "ohara", 5)
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected directory-level match for file history")
	}
}

func TestFileHistoryExcludesSupersededLinkedMemories(t *testing.T) {
	s := newTestStore(t)
	currentID, _ := s.AddMemory(AddMemoryParams{
		ProjectID:     "ohara",
		Kind:          MemoryKindProcedure,
		Title:         "Current router checklist",
		Body:          "Use internal/http/router.go context wiring checklist.",
		AppliesToJSON: `{"files":["internal/http/router.go"]}`,
	})
	oldID, _ := s.AddMemory(AddMemoryParams{
		ProjectID:     "ohara",
		Kind:          MemoryKindProcedure,
		Title:         "Current router checklist",
		Body:          "Legacy checklist for old router wiring.",
		AppliesToJSON: `{"files":["internal/http/router.go"]}`,
	})
	if _, err := s.Exec(`UPDATE memory_items SET superseded_by = ? WHERE id = ?`, currentID, oldID); err != nil {
		t.Fatalf("mark superseded_by: %v", err)
	}

	items, err := s.FileHistory("internal/http/router.go", "ohara", 10)
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	for _, item := range items {
		if item.ID == oldID {
			t.Fatalf("superseded-linked memory %d should be excluded from file history", oldID)
		}
	}
}
