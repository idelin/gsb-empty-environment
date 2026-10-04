package main

import (
	"math/rand"
	"testing"
)

// TestStressLargerTrees runs the DP + script + applier pipeline on larger
// random trees (no exhaustive search) to catch panics and regressions.
func TestStressLargerTrees(t *testing.T) {
	labels := []string{"a", "b", "c", "d"}
	for seed := int64(0); seed < 10; seed++ {
		r := rand.New(rand.NewSource(1000 + seed))
		src := randTree(r, labels, 60)
		tgt := randTree(r, labels, 60)
		c := randCosts(r, labels)
		nc := normalizeCosts(c, src, tgt)
		dist, mapping := treeEditDistance(src, tgt, nc)
		checkMapping(t, src, tgt, mapping)
		ops, err := buildScript(src, tgt, mapping, nc)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		res, cost, err := ApplyScript(src, ops, c)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if cost != dist || !Equal(res, tgt) {
			t.Fatalf("seed %d: cost=%d dist=%d equal=%v", seed, cost, dist, Equal(res, tgt))
		}
	}
}
