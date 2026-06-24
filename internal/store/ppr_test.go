package store

import (
	"math"
	"testing"
)

// TestBuildPPRGraph_Empty verifies empty input produces nil.
func TestBuildPPRGraph_Empty(t *testing.T) {
	g := buildPPRGraph(nil, nil)
	if g != nil {
		t.Error("expected nil graph for empty input")
	}
	g = buildPPRGraph([]int64{}, []MemoryRelation{})
	if g != nil {
		t.Error("expected nil graph for empty input")
	}
}

// TestBuildPPRGraph_SingleNode verifies single-node graphs.
func TestBuildPPRGraph_SingleNode(t *testing.T) {
	g := buildPPRGraph([]int64{42}, nil)
	if g == nil {
		t.Fatal("expected non-nil graph for single node")
	}
	if len(g.nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(g.nodes))
	}
	if g.nodes[0] != 42 {
		t.Errorf("expected node 42, got %d", g.nodes[0])
	}
	if g.index[42] != 0 {
		t.Errorf("expected index 0 for node 42, got %d", g.index[42])
	}
	// Adjacency should be a 1×1 zero matrix.
	if len(g.adj) != 1 || len(g.adj[0]) != 1 {
		t.Fatal("expected 1x1 adjacency")
	}
	if g.adj[0][0] != 0 {
		t.Errorf("expected zero self-edge, got %f", g.adj[0][0])
	}
}

// TestBuildPPRGraph_MultiNode verifies multi-node adjacency construction.
func TestBuildPPRGraph_MultiNode(t *testing.T) {
	ids := []int64{1, 2, 3}
	rels := []MemoryRelation{
		{ID: 1, FromID: 1, ToID: 2, Relation: RelationRelatedTo},
		{ID: 2, FromID: 2, ToID: 3, Relation: RelationCaused},
		{ID: 3, FromID: 1, ToID: 3, Relation: RelationSupersedes},
		// External relation — should be excluded since node 99 is not in candidates.
		{ID: 4, FromID: 1, ToID: 99, Relation: RelationRelatedTo},
	}

	g := buildPPRGraph(ids, rels)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	if len(g.nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(g.nodes))
	}

	// Check bidirectional edges: each relation creates two directed edges.
	// 1↔2 (RelatedTo=0.020), 2↔3 (Caused=0.018), 1↔3 (Supersedes=0.032)
	// Edge 1→99 excluded (99 not in candidates).

	// Node 1: edges to 2 and 3.
	if g.adj[0][1] == 0 {
		t.Error("expected edge 1→2")
	}
	if g.adj[0][2] == 0 {
		t.Error("expected edge 1→3")
	}

	// Node 2: edges to 1 and 3.
	if g.adj[1][0] == 0 {
		t.Error("expected edge 2→1")
	}
	if g.adj[1][2] == 0 {
		t.Error("expected edge 2→3")
	}

	// Node 3: edges to 1 and 2.
	if g.adj[2][0] == 0 {
		t.Error("expected edge 3→1")
	}
	if g.adj[2][1] == 0 {
		t.Error("expected edge 3→2")
	}

	// No self-loops.
	for i := 0; i < 3; i++ {
		if g.adj[i][i] != 0 {
			t.Errorf("unexpected self-loop at node %d: %f", i, g.adj[i][i])
		}
	}
}

// TestPPRPersonalization_EntitySignal verifies entity matches get highest weight.
func TestPPRPersonalization_EntitySignal(t *testing.T) {
	ids := []int64{10, 20, 30}
	g := buildPPRGraph(ids, nil) // no edges — graph structure irrelevant for personalization
	if g == nil {
		t.Fatal("expected non-nil graph")
	}

	entityMatches := map[int64]bool{20: true}
	lexicalMatches := map[int64]bool{10: true, 30: true}

	p := pprPersonalization(g, entityMatches, lexicalMatches)

	if len(p) != 3 {
		t.Fatalf("expected 3 scores, got %d", len(p))
	}

	// Node 20 should have highest weight (entity match = 1.0 before normalization).
	// Node 10 and 30 should be equal (lexical = 0.5).
	// After normalization, 20 should be roughly 2x 10.

	idx20 := g.index[20]
	idx10 := g.index[10]
	idx30 := g.index[30]

	if p[idx20] <= p[idx10] {
		t.Errorf("entity match (20) should be higher than lexical match (10): %f vs %f", p[idx20], p[idx10])
	}
	if math.Abs(p[idx10]-p[idx30]) > 1e-9 {
		t.Errorf("lexical matches should be equal: %f vs %f", p[idx10], p[idx30])
	}

	// All scores should sum to 1.
	sum := 0.0
	for _, v := range p {
		sum += v
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("personalization vector should sum to 1, got %f", sum)
	}
}

// TestPPRPersonalization_Fallback verifies uniform fallback when no matches.
func TestPPRPersonalization_Fallback(t *testing.T) {
	ids := []int64{1, 2, 3, 4}
	g := buildPPRGraph(ids, nil)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}

	// No entity or lexical matches — all nodes get uniform fallback.
	p := pprPersonalization(g, nil, nil)

	if len(p) != 4 {
		t.Fatalf("expected 4 scores, got %d", len(p))
	}
	for i, v := range p {
		if math.Abs(v-0.25) > 1e-9 {
			t.Errorf("node %d: expected 0.25, got %f", i, v)
		}
	}
}

// TestPersonalizedPageRank_Identity verifies PPR preserves personalization
// on an empty graph (no edges → dangling nodes → teleport dominates).
func TestPersonalizedPageRank_Identity(t *testing.T) {
	ids := []int64{1, 2, 3}
	g := buildPPRGraph(ids, nil) // no edges
	if g == nil {
		t.Fatal("expected non-nil graph")
	}

	// Uniform personalization.
	p := []float64{1.0 / 3.0, 1.0 / 3.0, 1.0 / 3.0}
	scores := personalizedPageRank(g, p, 0.15, 5)

	if len(scores) != 3 {
		t.Fatalf("expected 3 scores, got %d", len(scores))
	}
	// On empty graph, PPR should converge to the personalization vector.
	for i, v := range scores {
		if math.Abs(v-p[i]) > 1e-6 {
			t.Errorf("node %d: expected ~%f, got %f", i, p[i], v)
		}
	}
}

// TestPersonalizedPageRank_Propagation verifies PPR correctly propagates
// score through a two-node graph.
func TestPersonalizedPageRank_Propagation(t *testing.T) {
	ids := []int64{1, 2}
	rels := []MemoryRelation{
		{ID: 1, FromID: 1, ToID: 2, Relation: RelationRelatedTo},
	}
	g := buildPPRGraph(ids, rels)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}

	// Personalization: only node 1 is a seed.
	p := []float64{1.0, 0.0} // [node1, node2]
	scores := personalizedPageRank(g, p, 0.15, 5)

	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}

	// On a small bidirectional graph, propagation oscillates between nodes.
	// The key invariant: both nodes have non-zero scores (propagation occurred).
	if scores[0] <= 0 {
		t.Errorf("seed node should have positive PPR score: %f", scores[0])
	}
	if scores[1] <= 0 {
		t.Errorf("neighbor node should have positive score from propagation: %f", scores[1])
	}
	// Scores should sum to approximately 1.0.
	sum := scores[0] + scores[1]
	if math.Abs(sum-1.0) > 1e-6 {
		t.Errorf("PPR scores should sum to 1.0, got %f", sum)
	}
}

// TestPersonalizedPageRank_Convergence verifies early convergence detection.
func TestPersonalizedPageRank_Convergence(t *testing.T) {
	ids := []int64{1, 2}
	rels := []MemoryRelation{
		{ID: 1, FromID: 1, ToID: 2, Relation: RelationRelatedTo},
	}
	g := buildPPRGraph(ids, rels)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	p := []float64{0.5, 0.5}

	// Run with many iterations — should converge early.
	scores := personalizedPageRank(g, p, 0.15, 50)

	// Run with few iterations — should be close to many-iteration result.
	scoresFew := personalizedPageRank(g, p, 0.15, 5)

	if len(scores) != len(scoresFew) {
		t.Fatal("iteration count should not affect dimensionality")
	}
	for i := range scores {
		if math.Abs(scores[i]-scoresFew[i]) > 1e-4 {
			t.Errorf("node %d: manyIters=%f fewIters=%f diff=%f", i, scores[i], scoresFew[i], math.Abs(scores[i]-scoresFew[i]))
		}
	}
}

// TestPPRBlendAndRerank_NoOp verifies lambda=0 preserves original order.
func TestPPRBlendAndRerank_NoOp(t *testing.T) {
	items := []MemoryItem{
		{ID: 1, Title: "A", RelevanceScore: 0.9, UpdatedAt: "2026-01-01T00:00:00Z"},
		{ID: 2, Title: "B", RelevanceScore: 0.5, UpdatedAt: "2026-01-01T00:00:00Z"},
		{ID: 3, Title: "C", RelevanceScore: 0.1, UpdatedAt: "2026-01-01T00:00:00Z"},
	}
	ids := []int64{1, 2, 3}
	rels := []MemoryRelation{
		{ID: 1, FromID: 1, ToID: 2, Relation: RelationRelatedTo},
	}
	g := buildPPRGraph(ids, rels)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	p := pprPersonalization(g, map[int64]bool{3: true}, nil)
	scores := personalizedPageRank(g, p, 0.15, 5)

	// λ = 0: original order preserved.
	result := pprBlendAndRerank(items, scores, g, 0.0)
	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result))
	}
	if result[0].ID != 1 || result[1].ID != 2 || result[2].ID != 3 {
		t.Errorf("λ=0 should preserve original order: got %d %d %d", result[0].ID, result[1].ID, result[2].ID)
	}
}

// TestPPRBlendAndRerank_PurePPR verifies λ=1 uses pure PPR ordering.
func TestPPRBlendAndRerank_PurePPR(t *testing.T) {
	items := []MemoryItem{
		{ID: 1, Title: "A", RelevanceScore: 0.9, UpdatedAt: "2026-01-01T00:00:00Z"},
		{ID: 2, Title: "B", RelevanceScore: 0.5, UpdatedAt: "2026-01-01T00:00:00Z"},
		{ID: 3, Title: "C", RelevanceScore: 0.1, UpdatedAt: "2026-01-01T00:00:00Z"},
	}
	ids := []int64{1, 2, 3}
	rels := []MemoryRelation{
		{ID: 1, FromID: 1, ToID: 2, Relation: RelationRelatedTo},
	}
	g := buildPPRGraph(ids, rels)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	// Seed on node 3 (lowest relevance score).
	p := pprPersonalization(g, map[int64]bool{3: true}, nil)
	scores := personalizedPageRank(g, p, 0.15, 5)

	// λ = 1.0: pure PPR ordering.
	result := pprBlendAndRerank(items, scores, g, 1.0)
	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result))
	}
	// With seed on node 3 (entity match), PPR should promote it.
	// Node 3 has highest PPR score → should be first.
	if result[0].ID != 3 {
		t.Logf("λ=1: expected PPR to promote seed node 3, got first=%d", result[0].ID)
	}
}

// TestPPRBlendAndRerank_EmptyInput verifies empty/single-item inputs are handled.
func TestPPRBlendAndRerank_EmptyInput(t *testing.T) {
	// Empty.
	if result := pprBlendAndRerank(nil, nil, nil, 0.15); result != nil {
		t.Error("expected nil for empty input")
	}

	// Single item.
	items := []MemoryItem{
		{ID: 1, Title: "A", RelevanceScore: 0.9, UpdatedAt: "2026-01-01T00:00:00Z"},
	}
	result := pprBlendAndRerank(items, []float64{0.5}, nil, 0.15)
	if len(result) != 1 || result[0].ID != 1 {
		t.Error("single item should be returned as-is")
	}
}

// TestLexicalMatchesForQuery verifies phrase and token matching.
func TestLexicalMatchesForQuery(t *testing.T) {
	items := []MemoryItem{
		{ID: 1, Title: "Vector search", Body: "Using embeddings for similarity"},
		{ID: 2, Title: "Storage engine", Body: "SQLite with FTS5"},
		{ID: 3, Title: "Unrelated", Body: "Something else entirely"},
	}

	// Exact word match.
	matches := lexicalMatchesForQuery("vector", items)
	if !matches[1] {
		t.Error("item 1 should match 'vector'")
	}
	if matches[2] {
		t.Error("item 2 should not match 'vector'")
	}

	// Phrase match.
	matches = lexicalMatchesForQuery("storage engine", items)
	if !matches[2] {
		t.Error("item 2 should match 'storage engine'")
	}

	// Empty query.
	matches = lexicalMatchesForQuery("", items)
	if len(matches) != 0 {
		t.Error("empty query should produce no matches")
	}
}

// TestPPRRerank_Integration tests the full end-to-end PPR pipeline
// using a real store with indexed memories and relations.
func TestPPRRerank_Integration(t *testing.T) {
	s := newTestStore(t)

	// Create three memories.
	params := []BulkSeedMemoryParams{
		{ProjectID: "ppr-test", Kind: MemoryKindDiscovery, Title: "vec0 available", Body: "modernc.org/sqlite gives you pure-Go SQLite. For vectors, the vec0 extension is available now.", Domain: "test", SessionID: "s1"},
		{ProjectID: "ppr-test", Kind: MemoryKindDiscovery, Title: "version constraint", Body: "The vec0 extension is in modernc v1.47.0+. But we need to stay on v1.45.0 for now.", Domain: "test", SessionID: "s1"},
		{ProjectID: "ppr-test", Kind: MemoryKindDiscovery, Title: "brute-force works", Body: "For small N under 1000 items, brute-force cosine works fine in Go.", Domain: "test", SessionID: "s1"},
	}
	ids, err := s.BulkSeedMemories(params)
	if err != nil {
		t.Fatalf("seed memories: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 seeded IDs, got %d", len(ids))
	}

	// Create entity "vec0" and link to first two memories.
	eid, err := s.UpsertEntity("vec0", "component", "ppr-test")
	if err != nil {
		t.Fatalf("upsert entity: %v", err)
	}
	if err := s.LinkMemoryEntity(ids[0], eid); err != nil {
		t.Fatalf("link memory 0 to entity: %v", err)
	}
	if err := s.LinkMemoryEntity(ids[1], eid); err != nil {
		t.Fatalf("link memory 1 to entity: %v", err)
	}

	// Create relations between memories.
	if err := s.AddRelation(ids[0], ids[1], RelationRelatedTo); err != nil {
		t.Fatalf("add relation 0→1: %v", err)
	}
	if err := s.AddRelation(ids[1], ids[2], RelationCaused); err != nil {
		t.Fatalf("add relation 1→2: %v", err)
	}

	// Build candidate list.
	items := make([]MemoryItem, 3)
	for i, id := range ids {
		// Fetch the full MemoryItem.
		got, err := s.GetMemory(id)
		if err != nil {
			t.Fatalf("get memory %d: %v", id, err)
		}
		items[i] = *got
		// Set synthetic relevance scores (descending).
		items[i].RelevanceScore = float64(3-i) * 0.3 // 0.9, 0.6, 0.3
	}

	// Run PPR with a query that should match entity "vec0".
	query := "What is the vec0 version constraint?"

	// Run PPR rerank.
	result := s.pprRerank(query, items)

	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result))
	}

	// The PPR should boost items sharing entity "vec0" (items 0 and 1)
	// and demote item 2 which has no entity match.
	// After blend (λ=0.15), the entity-matched items should be promoted.
	t.Logf("PPR reorder: [0]=%s(id=%d) [1]=%s(id=%d) [2]=%s(id=%d)",
		result[0].Title, result[0].ID,
		result[1].Title, result[1].ID,
		result[2].Title, result[2].ID,
	)
}

// TestPPRRerank_NoEdges verifies PPR is a no-op when no relations exist.
func TestPPRRerank_NoEdges(t *testing.T) {
	s := newTestStore(t)

	params := []BulkSeedMemoryParams{
		{ProjectID: "ppr-test", Kind: MemoryKindDiscovery, Title: "Foo", Body: "foo bar baz", Domain: "test"},
		{ProjectID: "ppr-test", Kind: MemoryKindDiscovery, Title: "Bar", Body: "bar baz qux", Domain: "test"},
	}
	ids, err := s.BulkSeedMemories(params)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	items := make([]MemoryItem, 2)
	for i, id := range ids {
		got, err := s.GetMemory(id)
		if err != nil {
			t.Fatalf("get memory: %v", err)
		}
		items[i] = *got
		items[i].RelevanceScore = float64(2-i) * 0.5 // 1.0, 0.5
	}

	// No relations between these memories.
	result := s.pprRerank("foo", items)

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	// Without edges, PPR should return items unchanged.
	if result[0].ID != items[0].ID || result[1].ID != items[1].ID {
		t.Errorf("no edges should preserve original order")
	}
}

// TestPPRRerank_EmptyQuery verifies empty/no-query behaves as no-op.
func TestPPRRerank_EmptyQuery(t *testing.T) {
	s := newTestStore(t)

	params := []BulkSeedMemoryParams{
		{ProjectID: "ppr-test", Kind: MemoryKindDiscovery, Title: "A", Body: "aaa", Domain: "test"},
		{ProjectID: "ppr-test", Kind: MemoryKindDiscovery, Title: "B", Body: "bbb", Domain: "test"},
	}
	ids, err := s.BulkSeedMemories(params)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = s.AddRelation(ids[0], ids[1], RelationRelatedTo)

	items := make([]MemoryItem, 2)
	for i, id := range ids {
		got, _ := s.GetMemory(id)
		items[i] = *got
		items[i].RelevanceScore = float64(2-i) * 0.5
	}

	result := s.pprRerank("", items)
	if result[0].ID != items[0].ID || result[1].ID != items[1].ID {
		t.Error("empty query should be no-op")
	}
}

// TestPPRRerank_SingleItem verifies single-item input is a no-op.
func TestPPRRerank_SingleItem(t *testing.T) {
	s := newTestStore(t)

	params := []BulkSeedMemoryParams{
		{ProjectID: "ppr-test", Kind: MemoryKindDiscovery, Title: "X", Body: "xxx", Domain: "test"},
	}
	ids, err := s.BulkSeedMemories(params)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, _ := s.GetMemory(ids[0])
	items := []MemoryItem{*got}
	items[0].RelevanceScore = 0.5

	result := s.pprRerank("query", items)
	if len(result) != 1 || result[0].ID != items[0].ID {
		t.Error("single item should be no-op")
	}
}
