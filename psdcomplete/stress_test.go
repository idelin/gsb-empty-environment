package psdcomplete

import (
	"math/big"
	"testing"
	"time"
)

// 6 unknowns x 10 candidates = 1,000,000 combinations (the configured
// limit). The branch-and-bound solver must handle it quickly; the
// exhaustive verifier is exercised separately on small instances.
func TestStressMillionCombos(t *testing.T) {
	s := mustSpec(t, 4)
	c := make([]*big.Rat, 10)
	for i := range c {
		c[i] = Frac(int64(i-4), 2)
	}
	for i := 0; i < 4; i++ {
		mustFix(t, s, i, i, Rat(2))
	}
	mustCands(t, s, 0, 1, c...)
	mustCands(t, s, 0, 2, c...)
	mustCands(t, s, 0, 3, c...)
	mustCands(t, s, 1, 2, c...)
	mustCands(t, s, 1, 3, c...)
	mustCands(t, s, 2, 3, c...)
	if got := s.ComboCount(); got != 1_000_000 {
		t.Fatalf("combos = %d", got)
	}
	start := time.Now()
	r, err := Solve(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Solve: %s, nodes=%d leaves=%d pruned=%d",
		time.Since(start), r.NodesExpanded, r.LeavesEvaluated, r.PruneTotal)
	if !r.OK {
		t.Fatal("expected feasible")
	}
	if !r.Certificate.Valid() {
		t.Fatal("certificate invalid")
	}
}
