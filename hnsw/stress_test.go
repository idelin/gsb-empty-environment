package hnsw

import (
	"math/rand"
	"testing"
)

// TestRandomizedDeletionStress builds random 2D kNN graphs, repeatedly
// deletes random batches, and re-validates invariants, connectivity and
// exact nearest-neighbor ranking after every batch.
func TestRandomizedDeletionStress(t *testing.T) {
	for seed := int64(1); seed <= 12; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 30 + rng.Intn(20)
		// kNN with k up to 4 gives an undirected degree up to 8 before
		// deletion; M0=2M must therefore stay at or above 8 on import.
		m := 4 + rng.Intn(3)
		g := newTestGraph(t, m, rng.Intn(2) == 0)

		coords := make([]vec2, n)
		for id := 0; id < n; id++ {
			coords[id] = vec2{x: rng.Float64() * 20, y: rng.Float64() * 20}
			addNode(t, g, id, coords[id], 0)
		}
		for id := 0; id < n; id++ {
			for _, neighborID := range nearestIDs(coords, id, 3+rng.Intn(2)) {
				if err := g.AddEdge(0, id, neighborID); err != nil {
					t.Fatalf("seed %d AddEdge: %v", seed, err)
				}
			}
		}
		if err := g.CheckInvariants(); err != nil {
			t.Fatalf("seed %d build invariants: %v", seed, err)
		}

		remaining := map[int]struct{}{}
		for id := 0; id < n; id++ {
			remaining[id] = struct{}{}
		}
		for len(remaining) > 0 {
			batchSize := 1 + rng.Intn(4)
			if batchSize > len(remaining) {
				batchSize = len(remaining)
			}
			batch := make([]int, 0, batchSize)
			for id := range remaining {
				batch = append(batch, id)
				if len(batch) == batchSize {
					break
				}
			}
			if _, err := g.Delete(batch); err != nil {
				t.Fatalf("seed %d Delete %v: %v", seed, batch, err)
			}
			for _, id := range batch {
				delete(remaining, id)
			}

			if err := g.CheckInvariants(); err != nil {
				t.Fatalf("seed %d post-delete invariants: %v", seed, err)
			}
			if !g.Layer0Connected() {
				t.Fatalf("seed %d fragmented after deleting %v", seed, batch)
			}
			if g.Alive() != len(remaining) {
				t.Fatalf("seed %d alive count mismatch", seed)
			}

			if len(remaining) > 0 {
				q := vec2{x: rng.Float64() * 20, y: rng.Float64() * 20}
				k := 3
				if k >= len(remaining) {
					k = len(remaining)
				}
				got, err := g.KNN(q, k, len(remaining))
				if err != nil {
					t.Fatalf("seed %d KNN: %v", seed, err)
				}
				want := bruteForce(g, q, k)
				if len(got) != len(want) {
					t.Fatalf("seed %d KNN length = %d, want %d", seed, len(got), len(want))
				}
				for i := range want {
					if got[i].ID != want[i].ID || got[i].Distance != want[i].Distance {
						t.Fatalf("seed %d ranking = %v, want %v", seed, resultIDs(got), resultIDs(want))
					}
				}
			}
		}

		if _, ok := g.Entry(); ok {
			t.Fatalf("seed %d entry must be absent after full deletion", seed)
		}
	}
}
