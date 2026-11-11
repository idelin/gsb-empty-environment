package wafdecision

import (
	"math"
	"math/rand"
	"testing"
)

// bruteExpectedBox independently enumerates every selectivity-box vertex and
// computes expected costs directly, validating the linear DP implementation.
func bruteExpectedBox(g *Graph, costs []uint64) (lo, hi float64) {
	n := len(g.specs)
	lo, hi = math.MaxFloat64, -math.MaxFloat64
	vertices := 1 << uint(n)
	for v := 0; v < vertices; v++ {
		probs := make([]float64, n)
		for i, s := range g.specs {
			if v&(1<<uint(i)) != 0 {
				probs[i] = s.SelHi
			} else {
				probs[i] = s.SelLo
			}
		}
		e := g.expectedCostAt(costs, probs)
		lo = math.Min(lo, e)
		hi = math.Max(hi, e)
	}
	return lo, hi
}

func TestExpectedBoxMatchesBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	for iter := 0; iter < 200; iter++ {
		n := 1 + rng.Intn(7)
		specs := make([]LeafSpec, n)
		for i := 0; i < n; i++ {
			lo := rng.Float64()
			hi := lo + rng.Float64()*(1-lo)
			specs[i] = spec(i, uint64(rng.Intn(15)), lo, hi)
		}
		kids := make([]Formula, n)
		for i := range kids {
			kids[i] = NewLeaf(i)
		}
		var rule Formula
		if rng.Intn(2) == 0 {
			rule = AndOf(kids...)
		} else {
			rule = OrOf(kids...)
		}
		g, err := Compile(rule, specs)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		costs := make([]uint64, 1<<uint(n))
		for mask := range costs {
			a := map[int]bool{}
			for i, s := range g.specs {
				a[s.ID] = mask&(1<<uint(i)) != 0
			}
			det, calls := map[int]Detector{}, map[int]int{}
			for _, s := range g.specs {
				det[s.ID] = detectorStub(a, calls, s.ID)
			}
			_, st := g.Eval(det)
			costs[mask] = st.TotalCost
		}
		wantLo, wantHi := bruteExpectedBox(g, costs)
		gotLo, gotHi := g.expectedCostBox(costs)
		if math.Abs(gotLo-wantLo) > 1e-9 || math.Abs(gotHi-wantHi) > 1e-9 {
			t.Fatalf("iter %d: DP (%v,%v) vs brute (%v,%v)",
				iter, gotLo, gotHi, wantLo, wantHi)
		}
	}
}
