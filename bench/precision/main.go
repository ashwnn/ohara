package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ashwnn/ohara/internal/store"
)

type fixtureCase struct {
	Query       string
	ExpectedIDs []int64
}

func main() {
	k := flag.Int("k", 3, "precision@k")
	flag.Parse()

	tmp, err := os.MkdirTemp("", "ohara-bench-precision-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	cfg := store.FallbackConfig(tmp)
	s, err := store.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new store: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	idJWT, _ := s.AddMemory(store.AddMemoryParams{ProjectID: "bench", Kind: store.MemoryKindBugfix, Title: "Fix JWT refresh race", Body: "Added mutex around refresh token rotation in middleware", Domain: "auth", Classification: "tactical"})
	idWAL, _ := s.AddMemory(store.AddMemoryParams{ProjectID: "bench", Kind: store.MemoryKindDecision, Title: "Use WAL mode", Body: "Always enable WAL for sqlite connections", Domain: "database", Classification: "foundational"})
	idRLS, _ := s.AddMemory(store.AddMemoryParams{ProjectID: "bench", Kind: store.MemoryKindProcedure, Title: "Enable RLS policy", Body: "When adding tenant table, create RLS policy and verify with integration test", Domain: "database", Classification: "foundational"})
	idRetry, _ := s.AddMemory(store.AddMemoryParams{ProjectID: "bench", Kind: store.MemoryKindPattern, Title: "HTTP retry backoff", Body: "Use exponential backoff with jitter for transient 5xx", Domain: "api", Classification: "tactical"})

	_ = idRetry

	cases := []fixtureCase{
		{Query: "jwt refresh middleware race", ExpectedIDs: []int64{idJWT}},
		{Query: "sqlite wal connection mode", ExpectedIDs: []int64{idWAL}},
		{Query: "tenant rls policy procedure", ExpectedIDs: []int64{idRLS}},
	}

	var total float64
	for _, tc := range cases {
		results, err := s.SearchMemories(tc.Query, "bench", "", "", "", store.MemoryStatusActive, *k, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "search %q: %v\n", tc.Query, err)
			os.Exit(1)
		}
		hits := 0
		for i := 0; i < len(results) && i < *k; i++ {
			for _, expected := range tc.ExpectedIDs {
				if results[i].ID == expected {
					hits++
					break
				}
			}
		}
		total += float64(hits) / float64(*k)
	}

	precision := total / float64(len(cases))
	fmt.Printf("precision@%d = %.4f (%d queries)\n", *k, precision, len(cases))
}
