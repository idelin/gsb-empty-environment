package dom

import (
	"math/rand"
	"testing"
)

// TestRandomGraphsCrossCheck builds deterministic pseudo-random
// graphs (including self-loops, duplicate edge attempts, cycles, and
// unreachable nodes) and cross-checks every dominance pair against
// the independent removal-based checker.
func TestRandomGraphsCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 200; trial++ {
		n := 2 + rng.Intn(9)
		g := NewGraph(0)
		for i := 1; i < n; i++ {
			g.AddNode(i)
		}
		edges := n + rng.Intn(2*n+1)
		for i := 0; i < edges; i++ {
			g.AddEdge(rng.Intn(n), rng.Intn(n)) // may be duplicate or self-loop
		}
		crossCheck(t, g)
	}
}
