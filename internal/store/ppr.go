package store

import (
	"math"
	"sort"
	"strings"
)

// pprGraph is an in-memory adjacency matrix over a small candidate set (n ≤ 200).
// Weights are derived from typed memory_relations + optional entity co-occurrence.
type pprGraph struct {
	nodes []int64       // candidate memory IDs in matrix order
	index map[int64]int // ID → matrix position
	adj   [][]float64   // n×n dense adjacency (sparse in practice, but n is small)
}

// pprEdge represents a directed weighted edge between two candidate memory nodes.
type pprEdge struct {
	from   int64
	to     int64
	weight float64
}

// buildPPRGraph constructs an adjacency graph over the given candidate IDs
// using relation edges loaded from the database. Only edges where both ends
// are in candidateIDs are included.
func buildPPRGraph(candidateIDs []int64, relations []MemoryRelation) *pprGraph {
	n := len(candidateIDs)
	if n == 0 {
		return nil
	}

	idx := make(map[int64]int, n)
	for i, id := range candidateIDs {
		idx[id] = i
	}

	// Collect directed edges from relations.
	edges := make([]pprEdge, 0, len(relations)*2)
	idSet := make(map[int64]bool, n)
	for _, id := range candidateIDs {
		idSet[id] = true
	}
	for _, rel := range relations {
		if !idSet[rel.FromID] || !idSet[rel.ToID] {
			continue
		}
		w := relationTypeWeight(rel.Relation)
		edges = append(edges, pprEdge{from: rel.FromID, to: rel.ToID, weight: w})
		// Relations are directional but for graph propagation we treat them
		// as bidirectional with same weight (both ends benefit from the link).
		edges = append(edges, pprEdge{from: rel.ToID, to: rel.FromID, weight: w})
	}

	// Build adjacency matrix.
	adj := make([][]float64, n)
	for i := range adj {
		adj[i] = make([]float64, n)
	}
	for _, e := range edges {
		fi, fok := idx[e.from]
		ti, tok := idx[e.to]
		if fok && tok && fi != ti {
			adj[fi][ti] += e.weight
		}
	}

	return &pprGraph{
		nodes: candidateIDs,
		index: idx,
		adj:   adj,
	}
}

// pprPersonalization builds the personalization vector p for PPR.
// Seeds come from three sources (in priority order):
//  1. entityMatches: IDs that share entities extracted from the query (weight 1.0)
//  2. lexicalMatches: IDs that matched query terms via FTS5 (weight 0.5)
//  3. uniformFallback: all IDs get a small baseline (weight 0.1)
func pprPersonalization(g *pprGraph, entityMatches, lexicalMatches map[int64]bool) []float64 {
	n := len(g.nodes)
	p := make([]float64, n)

	if n == 0 {
		return p
	}

	// Layer 1: entity matches — strongest signal.
	for _, id := range g.nodes {
		if entityMatches[id] {
			p[g.index[id]] = 1.0
		}
	}

	// Layer 2: lexical matches — moderate signal.
	for _, id := range g.nodes {
		if lexicalMatches[id] && p[g.index[id]] < 0.5 {
			p[g.index[id]] = 0.5
		}
	}

	// Layer 3: uniform fallback — ensures dangling nodes get some mass.
	for _, id := range g.nodes {
		if p[g.index[id]] < 0.1 {
			p[g.index[id]] = 0.1
		}
	}

	// Normalize to sum=1.
	sum := 0.0
	for _, v := range p {
		sum += v
	}
	if sum > 0 {
		for i := range p {
			p[i] /= sum
		}
	} else {
		// Degenerate case: uniform distribution.
		for i := range p {
			p[i] = 1.0 / float64(n)
		}
	}

	return p
}

// personalizedPageRank runs bounded iterative Personalized PageRank.
// α (alpha) is the teleport probability (default 0.15).
// maxIters caps iterations. Small graphs converge fast.
// Returns PPR scores in the same order as g.nodes.
func personalizedPageRank(g *pprGraph, personalization []float64, alpha float64, maxIters int) []float64 {
	n := len(g.nodes)
	if n == 0 || len(personalization) != n {
		return nil
	}

	// Row-normalize adjacency: Â[i][j] = A[i][j] / sum(A[i][*]).
	// Dangling nodes (zero out-degree) are treated as teleport-only.
	normAdj := make([][]float64, n)
	outDegree := make([]float64, n)
	for i := range g.adj {
		for j := range g.adj[i] {
			outDegree[i] += g.adj[i][j]
		}
	}
	for i := range g.adj {
		normAdj[i] = make([]float64, n)
		if outDegree[i] > 0 {
			inv := 1.0 / outDegree[i]
			for j := range g.adj[i] {
				normAdj[i][j] = g.adj[i][j] * inv
			}
		}
		// dangling node: outDegree=0 → normAdj row is all zeros
	}

	// Initialize score vector with personalization.
	s := make([]float64, n)
	copy(s, personalization)

	oneMinusAlpha := 1.0 - alpha

	for iter := 0; iter < maxIters; iter++ {
		next := make([]float64, n)

		// (1-α) × Âᵀ × s + α × p
		// For each target node j, sum over source nodes i:
		//   next[j] = (1-α) * Σᵢ s[i] * Â[i][j] + α * p[j]
		for i := 0; i < n; i++ {
			if s[i] == 0 {
				continue
			}
			// Distribute s[i] along outgoing edges.
			if outDegree[i] > 0 {
				for j := 0; j < n; j++ {
					if normAdj[i][j] > 0 {
						next[j] += oneMinusAlpha * s[i] * normAdj[i][j]
					}
				}
			} else {
				// Dangling node: distribute uniformly to all nodes.
				fraction := oneMinusAlpha * s[i] / float64(n)
				for j := 0; j < n; j++ {
					next[j] += fraction
				}
			}
		}

		// Add teleport term.
		for j := 0; j < n; j++ {
			next[j] += alpha * personalization[j]
		}

		// Convergence check: L1 distance.
		delta := 0.0
		for j := 0; j < n; j++ {
			delta += math.Abs(next[j] - s[j])
		}
		s = next
		if delta < 1e-6 {
			break
		}
	}

	return s
}

// pprBlendAndRerank blends PPR scores with original relevance scores and re-sorts.
// λ (lambda) controls PPR influence: 0 = original ordering, 1 = pure PPR.
// Default λ = 0.15.
func pprBlendAndRerank(items []MemoryItem, pprScores []float64, g *pprGraph, lambda float64) []MemoryItem {
	if len(items) == 0 || g == nil || len(pprScores) != len(g.nodes) {
		return items
	}
	if lambda <= 0 {
		return items
	}
	if lambda > 1 {
		lambda = 1
	}

	// Normalize PPR scores to [0,1] for stable blending.
	maxPPR := 0.0
	for _, v := range pprScores {
		if v > maxPPR {
			maxPPR = v
		}
	}
	normPPR := make(map[int64]float64, len(g.nodes))
	if maxPPR > 0 {
		for i, id := range g.nodes {
			normPPR[id] = pprScores[i] / maxPPR
		}
	}

	// Normalize original relevance scores to [0,1].
	maxOrig := 0.0
	minOrig := math.MaxFloat64
	for _, item := range items {
		if item.RelevanceScore > maxOrig {
			maxOrig = item.RelevanceScore
		}
		if item.RelevanceScore < minOrig {
			minOrig = item.RelevanceScore
		}
	}
	origRange := maxOrig - minOrig
	if origRange <= 0 {
		origRange = 1
	}

	// Build ID→item map for re-sorting.
	itemMap := make(map[int64]MemoryItem, len(items))
	for _, item := range items {
		itemMap[item.ID] = item
	}

	// Compute blended scores.
	type blended struct {
		item  MemoryItem
		score float64
	}
	blendedList := make([]blended, 0, len(items))
	for _, item := range items {
		origNorm := (item.RelevanceScore - minOrig) / origRange
		pprNorm := normPPR[item.ID]
		blendedScore := (1-lambda)*origNorm + lambda*pprNorm
		blendedList = append(blendedList, blended{item: item, score: blendedScore})
	}

	// Sort by blended score descending, fallback to original ordering.
	sort.SliceStable(blendedList, func(i, j int) bool {
		if math.Abs(blendedList[i].score-blendedList[j].score) < 1e-9 {
			if blendedList[i].item.UpdatedAt == blendedList[j].item.UpdatedAt {
				return blendedList[i].item.ID < blendedList[j].item.ID
			}
			return blendedList[i].item.UpdatedAt > blendedList[j].item.UpdatedAt
		}
		return blendedList[i].score > blendedList[j].score
	})

	out := make([]MemoryItem, len(blendedList))
	for i, b := range blendedList {
		out[i] = b.item
	}
	return out
}

// loadRelationsForIDs loads all memory_relations where either end is in the
// given set of IDs. Returns only relations where both ends exist (for graph integrity).
func (s *Store) loadRelationsForIDs(ids []int64) ([]MemoryRelation, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	ph := make([]string, len(ids))
	args := make([]any, 0, len(ids)*2)
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}
	args = append(args, args...) // duplicate for both IN clauses

	q := `SELECT id, from_obs_id, to_obs_id, relation, created_at
		FROM memory_relations
		WHERE from_obs_id IN (` + strings.Join(ph, ",") + `)
		   OR to_obs_id IN (` + strings.Join(ph, ",") + `)`

	rows, err := s.queryItHook(s.db, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rels []MemoryRelation
	for rows.Next() {
		var rel MemoryRelation
		if err := rows.Scan(&rel.ID, &rel.FromID, &rel.ToID, &rel.Relation, &rel.CreatedAt); err != nil {
			continue
		}
		rels = append(rels, rel)
	}
	return rels, rows.Err()
}

// entityMatchesForQuery returns memory IDs that share entities extracted from
// the query string. Uses the deterministic ExtractEntitiesHeuristic + obs_entities table.
func (s *Store) entityMatchesForQuery(query string, candidateIDs []int64) (map[int64]bool, error) {
	if len(candidateIDs) == 0 {
		return nil, nil
	}

	entities := ExtractEntitiesHeuristic(query)
	if len(entities) == 0 {
		return nil, nil
	}

	// Build placeholders for entities.
	ePh := make([]string, len(entities))
	eArgs := make([]any, len(entities))
	for i, e := range entities {
		ePh[i] = "?"
		eArgs[i] = strings.ToLower(e)
	}

	// Build placeholders for candidate IDs.
	cPh := make([]string, len(candidateIDs))
	cArgs := make([]any, len(candidateIDs))
	for i, id := range candidateIDs {
		cPh[i] = "?"
		cArgs[i] = id
	}

	q := `SELECT DISTINCT oe.obs_id
		FROM obs_entities oe
		JOIN entities e ON e.id = oe.entity_id
		WHERE LOWER(e.name) IN (` + strings.Join(ePh, ",") + `)
		  AND oe.obs_id IN (` + strings.Join(cPh, ",") + `)`

	args := append(eArgs, cArgs...)
	rows, err := s.queryItHook(s.db, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matches := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		matches[id] = true
	}
	return matches, rows.Err()
}

// lexicalMatchesForQuery returns candidate IDs that have non-zero FTS5 rank
// for the given query. Used as a moderate signal in the personalization vector.
// This is a simple heuristic: we check if each candidate's title+body contains
// any query term. Operates in-memory over already-fetched candidates.
func lexicalMatchesForQuery(query string, items []MemoryItem) map[int64]bool {
	if len(items) == 0 || query == "" {
		return nil
	}

	queryLower := strings.ToLower(query)
	queryTokens := tokenizeForRerank(query)

	matches := make(map[int64]bool, len(items)/2)
	for _, item := range items {
		text := strings.ToLower(item.Title + " " + item.Body)
		for _, token := range queryTokens {
			if strings.Contains(text, token) {
				matches[item.ID] = true
				break
			}
		}
		// Also check for exact phrase matches.
		if !matches[item.ID] && strings.Contains(text, queryLower) {
			matches[item.ID] = true
		}
	}
	return matches
}

// pprRerank runs the full PPR rerank pipeline on a candidate list.
// It is the single entry point wired into the hybrid search path.
// Returns reordered items. When the graph is empty or single-node,
// returns items unchanged (no-op).
func (s *Store) pprRerank(query string, items []MemoryItem) []MemoryItem {
	if len(items) <= 1 {
		return items
	}
	if query == "" {
		return items
	}

	// Step 1: Collect candidate IDs and load relations.
	candidateIDs := make([]int64, len(items))
	for i, item := range items {
		candidateIDs[i] = item.ID
	}

	relations, err := s.loadRelationsForIDs(candidateIDs)
	if err != nil || len(relations) == 0 {
		// No edges → PPR is equivalent to personalization-only.
		// Since personalization is weak without graph propagation,
		// return items unchanged to avoid noise.
		return items
	}

	// Step 2: Build graph.
	g := buildPPRGraph(candidateIDs, relations)
	if g == nil {
		return items
	}

	// Step 3: Build personalization vector.
	entityMatches, err := s.entityMatchesForQuery(query, candidateIDs)
	if err != nil {
		entityMatches = nil
	}
	lexicalMatches := lexicalMatchesForQuery(query, items)
	p := pprPersonalization(g, entityMatches, lexicalMatches)

	// Step 4: Run PPR.
	pprScores := personalizedPageRank(g, p, 0.15, 5)

	// Step 5: Blend and re-rank.
	const lambda = 0.15
	return pprBlendAndRerank(items, pprScores, g, lambda)
}
