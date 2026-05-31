package store

import (
	"testing"
)

func TestUpsertEntity_NewInsertReturnsID(t *testing.T) {
	s := newTestStore(t)

	id, err := s.UpsertEntity("test-entity-new", "token", "test-project")
	if err != nil {
		t.Fatalf("first UpsertEntity: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}
}

func TestUpsertEntity_RepeatSameID(t *testing.T) {
	s := newTestStore(t)
	const name, typ, proj = "repeat-entity", "token", "test-project"

	id1, err := s.UpsertEntity(name, typ, proj)
	if err != nil {
		t.Fatalf("first UpsertEntity: %v", err)
	}

	id2, err := s.UpsertEntity(name, typ, proj)
	if err != nil {
		t.Fatalf("second UpsertEntity: %v", err)
	}

	if id1 != id2 {
		t.Fatalf("expected same id on repeat, got %d != %d", id1, id2)
	}
}

func TestUpsertEntity_NoDuplicateRows(t *testing.T) {
	s := newTestStore(t)
	const name, typ, proj = "no-dup-entity", "token", "test-project"

	for i := 0; i < 5; i++ {
		if _, err := s.UpsertEntity(name, typ, proj); err != nil {
			t.Fatalf("UpsertEntity call %d: %v", i, err)
		}
	}

	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM entities WHERE name = ? AND type = ? AND project_key = ?`,
		name, typ, proj,
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, found %d duplicate(s)", count)
	}
}

func TestUpsertEntity_LinkAfterUpsert(t *testing.T) {
	s := newTestStore(t)
	const name, typ, proj = "link-entity", "token", "test-project"

	eid, err := s.UpsertEntity(name, typ, proj)
	if err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}

	// Create a memory item to link against.
	memID, err := s.AddMemory(AddMemoryParams{
		ProjectID: proj,
		Kind:      MemoryKindDiscovery,
		Title:     "link test",
		Body:      "body",
		Domain:    "test",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	if err := s.LinkMemoryEntity(memID, eid); err != nil {
		t.Fatalf("LinkMemoryEntity: %v", err)
	}

	// Verify link exists.
	var linkCount int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM obs_entities WHERE obs_id = ? AND entity_id = ?`,
		memID, eid,
	).Scan(&linkCount); err != nil {
		t.Fatalf("link count: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("expected 1 link, got %d", linkCount)
	}

	// Upsert the same entity again (should return same id).
	eid2, err := s.UpsertEntity(name, typ, proj)
	if err != nil {
		t.Fatalf("second UpsertEntity: %v", err)
	}
	if eid2 != eid {
		t.Fatalf("repeat UpsertEntity returned different id: %d != %d", eid2, eid)
	}

	// Link again with same pair — should be idempotent.
	if err := s.LinkMemoryEntity(memID, eid2); err != nil {
		t.Fatalf("second LinkMemoryEntity: %v", err)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM obs_entities WHERE obs_id = ? AND entity_id = ?`,
		memID, eid,
	).Scan(&linkCount); err != nil {
		t.Fatalf("final link count: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("expected still 1 link, got %d", linkCount)
	}
}

func TestUpsertEntity_Validation(t *testing.T) {
	s := newTestStore(t)

	tests := []struct {
		name, typ, proj string
	}{
		{"", "token", "p"},
		{"n", "", "p"},
		{"n", "token", ""},
		{"", "", ""},
	}
	for i, tc := range tests {
		if _, err := s.UpsertEntity(tc.name, tc.typ, tc.proj); err == nil {
			t.Errorf("case %d: expected error for (name=%q, type=%q, project=%q)", i, tc.name, tc.typ, tc.proj)
		}
	}
}
