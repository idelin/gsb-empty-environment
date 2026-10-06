package permtest

import (
	"math"
	"math/big"
	"testing"
)

// bruteForceStratified is an independent implementation: recursive
// enumeration over per-stratum treated-cluster subsets with float64
// arithmetic, used only to cross-check StratifiedCluster.
func bruteForceStratified(strata []Stratum) (tail, total int) {
	weights := make([]float64, len(strata))
	grand := 0
	for s, st := range strata {
		n := 0
		for _, c := range st.Clusters {
			n += len(c.Values)
		}
		grand += n
		if st.Weight != nil {
			weights[s], _ = st.Weight.Float64()
		} else {
			weights[s] = float64(n)
		}
	}
	for s, st := range strata {
		if st.Weight == nil {
			weights[s] /= float64(grand)
		}
	}

	stat := func(assign [][]bool) float64 {
		tStat := 0.0
		for s, st := range strata {
			var ts, cs float64
			var tn, cn int
			for c, cl := range st.Clusters {
				for _, v := range cl.Values {
					if assign[s][c] {
						ts += v
					} else {
						cs += v
					}
				}
				if assign[s][c] {
					tn += len(cl.Values)
				} else {
					cn += len(cl.Values)
				}
			}
			tStat += weights[s] * (ts/float64(tn) - cs/float64(cn))
		}
		return tStat
	}

	assign := make([][]bool, len(strata))
	for s, st := range strata {
		assign[s] = make([]bool, len(st.Clusters))
		for c := 0; c < st.Treated; c++ {
			assign[s][c] = true
		}
	}
	obs := math.Abs(stat(assign))

	var rec func(s, c, left int)
	rec = func(s, c, left int) {
		if s == len(strata) {
			if math.Abs(stat(assign)) >= obs-1e-9 {
				tail++
			}
			total++
			return
		}
		if c == len(strata[s].Clusters) {
			if left == 0 {
				next := 0
				if s+1 < len(strata) {
					next = strata[s+1].Treated
				}
				rec(s+1, 0, next)
			}
			return
		}
		if left > 0 {
			assign[s][c] = true
			rec(s, c+1, left-1)
		}
		assign[s][c] = false
		rec(s, c+1, left)
	}
	rec(0, 0, strata[0].Treated)
	return tail, total
}

func TestStratifiedClusterHandChecked(t *testing.T) {
	// Stratum 1: clusters {0},{3},{6}, one treated; stratum 2: {10},{20},
	// one treated. Default weights 3/5 and 2/5. The six legal assignments
	// give T in {-6.7, 1.3, -4, 4, -1.3, 6.7}, so with |T_obs| = 6.7 the
	// tail holds exactly 2 of 6.
	strata := []Stratum{
		{Clusters: []Cluster{{Values: []float64{0}}, {Values: []float64{3}}, {Values: []float64{6}}}, Treated: 1},
		{Clusters: []Cluster{{Values: []float64{10}}, {Values: []float64{20}}}, Treated: 1},
	}
	res, err := StratifiedCluster(strata)
	if err != nil {
		t.Fatal(err)
	}
	if res.P.Num != 2 || res.P.Den != 6 {
		t.Fatalf("p = %v, want 2/6", res.P)
	}
	if r := res.P.Reduced(); r.Num != 1 || r.Den != 3 {
		t.Fatalf("reduced = %v, want 1/3", r)
	}
	want := big.NewRat(-67, 10)
	if res.Observed.Cmp(want) != 0 {
		t.Fatalf("observed = %v, want %v", res.Observed, want)
	}
	// Stratification count evidence.
	if res.Total != 6 {
		t.Fatalf("total = %d, want 6", res.Total)
	}
	if len(res.Strata) != 2 {
		t.Fatalf("strata evidence = %v", res.Strata)
	}
	if res.Strata[0].Assignments != 3 || res.Strata[1].Assignments != 2 {
		t.Fatalf("per-stratum assignments = %d, %d; want 3, 2",
			res.Strata[0].Assignments, res.Strata[1].Assignments)
	}
	if res.Strata[0].Weight.Cmp(big.NewRat(3, 5)) != 0 ||
		res.Strata[1].Weight.Cmp(big.NewRat(2, 5)) != 0 {
		t.Fatalf("default weights = %v, %v; want 3/5, 2/5",
			res.Strata[0].Weight, res.Strata[1].Weight)
	}
}

func TestStratifiedClusterCustomWeights(t *testing.T) {
	// Same shape with explicit rational weights 1/3, 2/3:
	// T_obs = (1/3)(1-3) + (2/3)(2-8) = -14/3; assignments give
	// T in {-14/3, 10/3, -10/3, 14/3}, tail 2 of 4.
	strata := []Stratum{
		{Clusters: []Cluster{{Values: []float64{1}}, {Values: []float64{3}}}, Treated: 1, Weight: big.NewRat(1, 3)},
		{Clusters: []Cluster{{Values: []float64{2}}, {Values: []float64{8}}}, Treated: 1, Weight: big.NewRat(2, 3)},
	}
	res, err := StratifiedCluster(strata)
	if err != nil {
		t.Fatal(err)
	}
	if res.P.Num != 2 || res.P.Den != 4 {
		t.Fatalf("p = %v, want 2/4", res.P)
	}
	if res.Observed.Cmp(big.NewRat(-14, 3)) != 0 {
		t.Fatalf("observed = %v, want -14/3", res.Observed)
	}
}

func TestStratifiedClusterDuplicatesByPosition(t *testing.T) {
	// Identical values in every cluster: deduplicating values would leave
	// nothing to enumerate, but positions give C(3,1) = 3 assignments.
	strata := []Stratum{
		{Clusters: []Cluster{
			{Values: []float64{5, 5}},
			{Values: []float64{5, 5}},
			{Values: []float64{5, 5}},
		}, Treated: 1},
	}
	res, err := StratifiedCluster(strata)
	if err != nil {
		t.Fatal(err)
	}
	if res.P.Den != 3 || res.P.Num != 3 {
		t.Fatalf("p = %v, want 3/3", res.P)
	}
	if res.Strata[0].Assignments != 3 {
		t.Fatalf("assignments = %d, want 3", res.Strata[0].Assignments)
	}
}

func TestStratifiedClusterOnlyLegalAssignments(t *testing.T) {
	// 3 clusters choose 1 treated, times 4 clusters choose 2 treated:
	// exactly 3*6 = 18 legal assignments, no more, no less.
	strata := []Stratum{
		{Clusters: []Cluster{{Values: []float64{1}}, {Values: []float64{2}}, {Values: []float64{4}}}, Treated: 1},
		{Clusters: []Cluster{{Values: []float64{1, 2}}, {Values: []float64{3}}, {Values: []float64{-2}}, {Values: []float64{8}}}, Treated: 2},
	}
	res, err := StratifiedCluster(strata)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 18 || res.P.Den != 18 {
		t.Fatalf("total = %d, den = %d; want 18", res.Total, res.P.Den)
	}
	if res.Strata[0].Assignments != 3 || res.Strata[1].Assignments != 6 {
		t.Fatalf("per-stratum assignments = %d, %d; want 3, 6",
			res.Strata[0].Assignments, res.Strata[1].Assignments)
	}
}

func TestStratifiedClusterAgainstBruteForce(t *testing.T) {
	seed := uint32(999)
	next := func(mod int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>16) % mod
	}
	for tc := 0; tc < 100; tc++ {
		nStrata := 1 + next(3)
		strata := make([]Stratum, nStrata)
		for s := range strata {
			nClusters := 2 + next(3)
			st := Stratum{Treated: 1 + next(nClusters-1)}
			for c := 0; c < nClusters; c++ {
				nVals := 1 + next(2)
				cl := Cluster{Values: make([]float64, nVals)}
				for i := range cl.Values {
					cl.Values[i] = float64(next(5) - 2) // duplicates + negatives
				}
				st.Clusters = append(st.Clusters, cl)
			}
			strata[s] = st
		}
		res, err := StratifiedCluster(strata)
		if err != nil {
			t.Fatal(err)
		}
		wt, wn := bruteForceStratified(strata)
		if res.P.Num != int64(wt) || res.P.Den != int64(wn) {
			t.Fatalf("case %d %+v: got %v, brute force %d/%d", tc, strata, res.P, wt, wn)
		}
		wantTotal := int64(1)
		for _, st := range strata {
			wantTotal *= binomial(len(st.Clusters), st.Treated)
		}
		if res.Total != wantTotal {
			t.Fatalf("case %d: total = %d, want %d", tc, res.Total, wantTotal)
		}
	}
}

func TestStratifiedClusterErrors(t *testing.T) {
	if _, err := StratifiedCluster(nil); err == nil {
		t.Fatal("no strata: want error")
	}
	one := []Stratum{{Clusters: []Cluster{{Values: []float64{1}}}, Treated: 1}}
	if _, err := StratifiedCluster(one); err == nil {
		t.Fatal("single cluster: want error")
	}
	bad := []Stratum{{Clusters: []Cluster{{Values: []float64{1}}, {Values: []float64{2}}}, Treated: 2}}
	if _, err := StratifiedCluster(bad); err == nil {
		t.Fatal("treated == clusters: want error")
	}
	empty := []Stratum{{Clusters: []Cluster{{Values: []float64{1}}, {Values: nil}}, Treated: 1}}
	if _, err := StratifiedCluster(empty); err == nil {
		t.Fatal("empty cluster: want error")
	}
	nan := []Stratum{{Clusters: []Cluster{{Values: []float64{math.NaN()}}, {Values: []float64{2}}}, Treated: 1}}
	if _, err := StratifiedCluster(nan); err == nil {
		t.Fatal("NaN: want error")
	}
}
