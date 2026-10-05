package main

import (
	"math/rand"
	"testing"
	"time"
)

func TestTenOrderPerformance(t *testing.T) {
	for _, seed := range []int64{7, 13, 99, 1234} {
		rng := rand.New(rand.NewSource(seed))
		in := randomInstance(rng, 10, true)
		if err := in.validate(); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		s := newSolver(in, triangleInequalityHolds(in.TravelTime))
		s.solve()
		el := time.Since(start)
		t.Logf("seed %d: vehicles=%d dist=%d optimal=%v expansions=%d in %v",
			seed, len(s.bestRoutes), s.bestTotal, !s.aborted, s.expansions, el)
		if !s.hasBest {
			t.Fatal("no solution")
		}
		if el > 30*time.Second {
			t.Fatalf("seed %d too slow: %v", seed, el)
		}
	}
}
