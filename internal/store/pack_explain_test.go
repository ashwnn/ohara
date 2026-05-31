package store

import "testing"

func TestBuildPackExplainIncludesScoreComponents(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-explain",
		Kind:           MemoryKindDecision,
		Scope:          MemoryScopeProject,
		Title:          "Use migration gate",
		Body:           "All schema migrations must run in CI before deploy.",
		Classification: "foundational",
		Domain:         "database",
	})
	if err != nil {
		t.Fatalf("AddMemory decision: %v", err)
	}

	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-explain",
		BudgetTokens: 300,
		Explain:      true,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if len(result.Explain) == 0 {
		t.Fatal("expected explain rows")
	}
	row := result.Explain[0]
	if row.MemoryID == 0 {
		t.Fatal("expected memory id in explain row")
	}
	if row.ScoreComponents == nil || len(row.ScoreComponents) == 0 {
		t.Fatal("expected score components in explain row")
	}
	required := []string{
		"base_retrieval_score",
		"rrf_score",
		"recency_boost",
		"utility_weight",
		"structural_weight",
		"kind_priority",
		"stale_penalty",
		"superseded_penalty",
		"expiry_penalty",
		"final_score",
	}
	for _, key := range required {
		if _, ok := row.ScoreComponents[key]; !ok {
			t.Fatalf("expected %s component", key)
		}
	}
	if row.TokenEstimate <= 0 {
		t.Fatal("expected token estimate")
	}
	if row.Reason == "" {
		t.Fatal("expected inclusion/exclusion reason")
	}
}

func TestBuildPackPrefersFoundationalOverObservational(t *testing.T) {
	s := newTestStore(t)

	foundationalID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-priority",
		Kind:           MemoryKindDecision,
		Scope:          MemoryScopeProject,
		Title:          "Canonical auth decision",
		Body:           "Use RS256 JWT for service-to-service auth.",
		Classification: "foundational",
		Domain:         "auth",
	})
	if err != nil {
		t.Fatalf("AddMemory foundational: %v", err)
	}
	_, err = s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-priority",
		Kind:           MemoryKindDiscovery,
		Scope:          MemoryScopeProject,
		Title:          "Auth log observation",
		Body:           "Saw transient timeout in one run.",
		Classification: "observational",
		Domain:         "auth",
	})
	if err != nil {
		t.Fatalf("AddMemory observational: %v", err)
	}

	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-priority",
		BudgetTokens: 220,
		Explain:      true,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if len(result.MemoryItems) == 0 {
		t.Fatal("expected selected memory items")
	}
	if result.MemoryItems[0].ID != foundationalID {
		t.Fatalf("expected foundational decision first, got id=%d", result.MemoryItems[0].ID)
	}
}

func TestBuildPackStructuralWeightBoostsConnectedMemory(t *testing.T) {
	s := newTestStore(t)

	centralID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-structural",
		Kind:           MemoryKindPattern,
		Scope:          MemoryScopeProject,
		Title:          "Central pattern",
		Body:           "Use shared handler middleware pattern.",
		Classification: "tactical",
		Domain:         "api",
	})
	if err != nil {
		t.Fatalf("AddMemory central: %v", err)
	}
	peerAID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-structural",
		Kind:           MemoryKindPattern,
		Scope:          MemoryScopeProject,
		Title:          "Peer pattern A",
		Body:           "Auth middleware detail.",
		Classification: "tactical",
		Domain:         "api",
	})
	if err != nil {
		t.Fatalf("AddMemory peer A: %v", err)
	}
	peerBID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-structural",
		Kind:           MemoryKindPattern,
		Scope:          MemoryScopeProject,
		Title:          "Peer pattern B",
		Body:           "Rate limiting detail.",
		Classification: "tactical",
		Domain:         "api",
	})
	if err != nil {
		t.Fatalf("AddMemory peer B: %v", err)
	}

	if err := s.AddRelation(centralID, peerAID, RelationRelatedTo); err != nil {
		t.Fatalf("AddRelation central->peerA: %v", err)
	}
	if err := s.AddRelation(centralID, peerBID, RelationImplements); err != nil {
		t.Fatalf("AddRelation central->peerB: %v", err)
	}

	eid, err := s.UpsertEntity("internal/api/router.go", "path", "pack-structural")
	if err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	if err := s.LinkMemoryEntity(centralID, eid); err != nil {
		t.Fatalf("LinkMemoryEntity central: %v", err)
	}
	if err := s.LinkMemoryEntity(peerAID, eid); err != nil {
		t.Fatalf("LinkMemoryEntity peerA: %v", err)
	}

	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-structural",
		BudgetTokens: 400,
		Explain:      true,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}

	scoreByID := map[int64]float64{}
	for _, row := range result.Explain {
		scoreByID[row.MemoryID] = row.Score
	}
	if scoreByID[centralID] <= scoreByID[peerBID] {
		t.Fatalf("expected structurally connected memory to outrank peer: central=%.4f peerB=%.4f", scoreByID[centralID], scoreByID[peerBID])
	}
}

func TestBuildPackExplainRespectsTokenBudget(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 6; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "pack-budget",
			Kind:           MemoryKindDecision,
			Scope:          MemoryScopeProject,
			Title:          "Budget decision",
			Body:           "This memory body is intentionally verbose to exercise truncation and budget compliance in explain mode.",
			Classification: "foundational",
			Domain:         "agent",
		})
		if err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}
	}

	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-budget",
		BudgetTokens: 120,
		Explain:      true,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if result.TokenCount > 120 {
		t.Fatalf("expected pack token count <= 120, got %d", result.TokenCount)
	}

	includedRows := 0
	for _, row := range result.Explain {
		if row.Included {
			includedRows++
			if row.TokenEstimate <= 0 {
				t.Fatalf("included row %d should have token estimate", row.MemoryID)
			}
		}
	}
	if includedRows != result.ItemCount {
		t.Fatalf("included explain rows (%d) should match item_count (%d)", includedRows, result.ItemCount)
	}
}

func TestBuildPackSessionScopedObservationalIncludedUnderTightBudget(t *testing.T) {
	s := newTestStore(t)

	// Add a foundational memory that would normally outrank observational noise.
	foundationalID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-session-scope",
		Kind:           MemoryKindDecision,
		Scope:          MemoryScopeProject,
		Title:          "Important auth decision",
		Body:           "Use rotating refresh tokens with replay protection.",
		Classification: "foundational",
		Domain:         "auth",
	})
	if err != nil {
		t.Fatalf("AddMemory foundational: %v", err)
	}

	// Add observational noise from an explicit session.
	obsID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-session-scope",
		Kind:           MemoryKindDiscovery,
		Scope:          MemoryScopeProject,
		Title:          "Session scratch notes",
		Body:           "Random ideas about auth middleware, maybe retry.",
		Classification: "observational",
		Domain:         "auth",
		SessionID:      "sess-active",
	})
	if err != nil {
		t.Fatalf("AddMemory observational: %v", err)
	}

	// Build pack with explicit session scope and tight budget.
	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-session-scope",
		SessionID:    "sess-active",
		BudgetTokens: 150,
		Explain:      true,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}

	// The observational item should be included because the session matches.
	foundObs := false
	for _, item := range result.MemoryItems {
		if item.ID == obsID {
			foundObs = true
			break
		}
	}
	if !foundObs {
		t.Fatalf("expected session-scoped observational memory (id=%d) to be included; included ids=%v", obsID, memoryItemIDs(result.MemoryItems))
	}

	// The foundational memory may or may not be included depending on budget, but
	// the observational item must appear.
	_ = foundationalID
}

func TestBuildPackDefaultExcludesObservationalWithoutSessionScope(t *testing.T) {
	s := newTestStore(t)

	foundationalID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-default-excl",
		Kind:           MemoryKindDecision,
		Scope:          MemoryScopeProject,
		Title:          "Canonical decision",
		Body:           "Canonical project decision body.",
		Classification: "foundational",
		Domain:         "agent",
	})
	if err != nil {
		t.Fatalf("AddMemory foundational: %v", err)
	}

	obsID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "pack-default-excl",
		Kind:           MemoryKindDiscovery,
		Scope:          MemoryScopeProject,
		Title:          "Transient observation",
		Body:           "Debug notes for a specific run.",
		Classification: "observational",
		Domain:         "agent",
		SessionID:      "sess-ephemeral",
	})
	if err != nil {
		t.Fatalf("AddMemory observational: %v", err)
	}

	// Default pack: no session scope — observational items must be excluded.
	result, err := s.BuildPack(PackParams{
		ProjectID:    "pack-default-excl",
		BudgetTokens: 400,
		Explain:      true,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}

	// Observational item must NOT be included.
	for _, item := range result.MemoryItems {
		if item.ID == obsID {
			t.Fatalf("expected observational memory (id=%d) to be excluded from default pack", obsID)
		}
	}

	// Foundational item must be present.
	foundFoundational := false
	for _, item := range result.MemoryItems {
		if item.ID == foundationalID {
			foundFoundational = true
			break
		}
	}
	if !foundFoundational {
		t.Fatalf("expected foundational memory (id=%d) to be included", foundationalID)
	}
}

func memoryItemIDs(items []MemoryItem) []int64 {
	ids := make([]int64, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}
