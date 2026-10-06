package permtest

import (
	"math/big"
	"math/rand"
	"testing"
)

func weight(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

// bruteForceStratified is an independent reference: recursive enumeration
// of per-stratum treated-block subsets, float64 arithmetic, epsilon compare.
func bruteForceStratified(strata []Stratum) (tail, total int64) {
	obs := 0.0
	for _, st := range strata {
		w, _ := st.Weight.Float64()
		var tSum, tN, cSum, cN float64
		for _, b := range st.Blocks {
			for _, v := range b.Values {
				if b.Treated {
					tSum += v
					tN++
				} else {
					cSum += v
					cN++
				}
			}
		}
		obs += w * (tSum/tN - cSum/cN)
	}
	if obs < 0 {
		obs = -obs
	}
	const eps = 1e-9
	var walk func(s int, acc float64)
	walk = func(s int, acc float64) {
		if s == len(strata) {
			if acc < 0 {
				acc = -acc
			}
			if acc >= obs-eps {
				tail++
			}
			total++
			return
		}
		st := strata[s]
		w, _ := st.Weight.Float64()
		need := 0
		for _, b := range st.Blocks {
			if b.Treated {
				need++
			}
		}
		var tN, cN float64
		sums := make([]float64, len(st.Blocks))
		for i, b := range st.Blocks {
			for _, v := range b.Values {
				sums[i] += v
			}
			if b.Treated {
				tN += float64(len(b.Values))
			} else {
				cN += float64(len(b.Values))
			}
		}
		var tot float64
		for _, s2 := range sums {
			tot += s2
		}
		var rec func(pos, left int, acc2 float64)
		rec = func(pos, left int, tSum float64) {
			if left == 0 {
				diff := tSum/tN - (tot-tSum)/cN
				walk(s+1, acc+w*diff)
				return
			}
			for i := pos; i <= len(sums)-left; i++ {
				rec(i+1, left-1, tSum+sums[i])
			}
		}
		rec(0, need, 0)
	}
	walk(0, 0)
	return tail, total
}

func checkStratified(t *testing.T, strata []Stratum, wantTail, wantTotal int64, wantP string) *StratifiedResult {
	t.Helper()
	res, err := StratifiedExact(strata)
	if err != nil {
		t.Fatalf("StratifiedExact: %v", err)
	}
	if res.Tail.Int64() != wantTail || res.Total.Int64() != wantTotal {
		t.Errorf("tail/total = %v/%v, want %d/%d", res.Tail, res.Total, wantTail, wantTotal)
	}
	if got := res.PValue.RatString(); got != wantP {
		t.Errorf("p = %v, want %v", got, wantP)
	}
	// Enumeration evidence: per-stratum binomial counts, product = Total.
	prod := big.NewInt(1)
	for s, ev := range res.Evidence {
		want := new(big.Int).Binomial(int64(ev.Blocks), int64(ev.TreatedBlocks))
		if ev.Assignments.Cmp(want) != 0 {
			t.Errorf("stratum %d assignments = %v, want %v", s, ev.Assignments, want)
		}
		prod.Mul(prod, ev.Assignments)
	}
	if prod.Cmp(res.Total) != 0 {
		t.Errorf("product of stratum counts %v != total %v", prod, res.Total)
	}
	return res
}

func TestStratifiedHandComputed(t *testing.T) {
	// Stratum 1: blocks {1} vs {3}, weight 1/3. Stratum 2: {2} vs {4},
	// weight 2/3. Observed stat = (1/3)(1-3) + (2/3)(2-4) = -2.
	// Four assignments give stats -2, 2/3, -2/3, 2 -> tail 2/4 = 1/2.
	strata := []Stratum{
		{Blocks: []Block{
			{Values: []float64{1}, Treated: true},
			{Values: []float64{3}},
		}, Weight: weight(1, 3)},
		{Blocks: []Block{
			{Values: []float64{2}, Treated: true},
			{Values: []float64{4}},
		}, Weight: weight(2, 3)},
	}
	res := checkStratified(t, strata, 2, 4, "1/2")
	if res.ObservedStat.RatString() != "-2" {
		t.Errorf("observed stat = %v, want -2", res.ObservedStat)
	}
	if len(res.Evidence) != 2 {
		t.Fatalf("evidence len = %d", len(res.Evidence))
	}
	for s, ev := range res.Evidence {
		if ev.Blocks != 2 || ev.TreatedBlocks != 1 || ev.ControlBlocks != 1 || ev.Samples != 2 {
			t.Errorf("stratum %d evidence = %+v", s, ev)
		}
	}
}

func TestStratifiedBlocksMoveTogether(t *testing.T) {
	// Two-sample blocks switch as a unit: stratum 1 has blocks {1,2} and
	// {4,5} (weight 1), stratum 2 has {0} and {10} (weight 2).
	// Observed stat = (1.5-4.5) + 2*(0-10) = -23. Assignments give
	// -23, 17, -17, 23 -> tail 2/4 = 1/2.
	strata := []Stratum{
		{Blocks: []Block{
			{Values: []float64{1, 2}, Treated: true},
			{Values: []float64{4, 5}},
		}, Weight: weight(1, 1)},
		{Blocks: []Block{
			{Values: []float64{0}, Treated: true},
			{Values: []float64{10}},
		}, Weight: weight(2, 1)},
	}
	res := checkStratified(t, strata, 2, 4, "1/2")
	if res.ObservedStat.RatString() != "-23" {
		t.Errorf("observed stat = %v, want -23", res.ObservedStat)
	}
	if res.Evidence[0].Samples != 4 {
		t.Errorf("stratum 0 samples = %d, want 4", res.Evidence[0].Samples)
	}
}

func TestStratifiedUnequalTreatedCounts(t *testing.T) {
	// Stratum 1: 3 blocks, 2 treated -> C(3,2)=3 assignments.
	// Stratum 2: 4 blocks, 1 treated -> C(4,1)=4. Total 12.
	strata := []Stratum{
		{Blocks: []Block{
			{Values: []float64{-1, 0}, Treated: true},
			{Values: []float64{2}, Treated: true},
			{Values: []float64{2}}, // duplicate value, distinct position
		}, Weight: weight(1, 2)},
		{Blocks: []Block{
			{Values: []float64{-3}, Treated: true},
			{Values: []float64{1, 1}},
			{Values: []float64{4}},
			{Values: []float64{-2, 5}},
		}, Weight: weight(3, 4)},
	}
	res, err := StratifiedExact(strata)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total.Int64() != 12 {
		t.Fatalf("total = %v, want 12", res.Total)
	}
	tail, total := bruteForceStratified(strata)
	if res.Tail.Int64() != tail || total != 12 {
		t.Fatalf("tail = %v, want %v", res.Tail, tail)
	}
	if res.Evidence[0].Assignments.Int64() != 3 || res.Evidence[1].Assignments.Int64() != 4 {
		t.Errorf("evidence = %+v", res.Evidence)
	}
}

func TestStratifiedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 100; trial++ {
		nStrata := 1 + rng.Intn(3)
		strata := make([]Stratum, nStrata)
		for s := range strata {
			nBlocks := 2 + rng.Intn(3)
			nTreated := 1 + rng.Intn(nBlocks-1)
			blocks := make([]Block, nBlocks)
			perm := rng.Perm(nBlocks)
			for b := range blocks {
				nVal := 1 + rng.Intn(2)
				vals := make([]float64, nVal)
				for i := range vals {
					vals[i] = float64(rng.Intn(7) - 3) // duplicates, negatives
				}
				blocks[b] = Block{Values: vals, Treated: perm[b] < nTreated}
			}
			strata[s] = Stratum{
				Blocks: blocks,
				Weight: weight(int64(1+rng.Intn(3)), int64(1+rng.Intn(3))),
			}
		}
		res, err := StratifiedExact(strata)
		if err != nil {
			t.Fatal(err)
		}
		tail, total := bruteForceStratified(strata)
		if res.Tail.Int64() != tail || res.Total.Int64() != total {
			t.Errorf("trial %d: got %v/%v, want %d/%d", trial, res.Tail, res.Total, tail, total)
		}
	}
}

func TestStratifiedErrors(t *testing.T) {
	if _, err := StratifiedExact(nil); err == nil {
		t.Error("no strata: expected error")
	}
	good := Block{Values: []float64{1}, Treated: true}
	if _, err := StratifiedExact([]Stratum{{Blocks: []Block{good, {Values: []float64{2}}}}}); err == nil {
		t.Error("nil weight: expected error")
	}
	if _, err := StratifiedExact([]Stratum{{Blocks: []Block{good}, Weight: weight(1, 1)}}); err == nil {
		t.Error("single block: expected error")
	}
	if _, err := StratifiedExact([]Stratum{{Blocks: []Block{good, {Values: []float64{2}, Treated: true}}, Weight: weight(1, 1)}}); err == nil {
		t.Error("all treated: expected error")
	}
	if _, err := StratifiedExact([]Stratum{{Blocks: []Block{good, {Values: nil}}, Weight: weight(1, 1)}}); err == nil {
		t.Error("empty block: expected error")
	}
}
