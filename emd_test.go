package netsec

import (
	"math/big"
	"testing"
)

func ratOf(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad test rational: " + s)
	}
	return r
}

func ratInt64s(values ...int64) []*big.Rat {
	out := make([]*big.Rat, len(values))
	for i, v := range values {
		out[i] = big.NewRat(v, 1)
	}
	return out
}

func distFromCounts(counts []int64, total int64) []*big.Rat {
	return countsToRatDist(counts, total)
}

func TestWassersteinKnownValues(t *testing.T) {
	cases := []struct {
		name string
		a    []*big.Rat
		b    []*big.Rat
		want string
	}{
		{
			name: "identical distributions",
			a:    ratInt64s(1, 0, 0),
			b:    ratInt64s(1, 0, 0),
			want: "0",
		},
		{
			name: "endpoint split k=3",
			a:    ratInt64s(1, 0, 0),
			b:    ratInt64s(0, 0, 1),
			want: "2",
		},
		{
			name: "halves at adjacent positions",
			a:    []*big.Rat{ratOf("1/2"), ratOf("1/2")},
			b:    ratInt64s(0, 1),
			want: "1/2",
		},
		{
			name: "halves at endpoints k=3",
			a:    []*big.Rat{ratOf("1/2"), ratOf("0"), ratOf("1/2")},
			b:    ratInt64s(0, 0, 1),
			want: "1",
		},
		{
			name: "rational thirds",
			a:    []*big.Rat{ratOf("1/3"), ratOf("1/3"), ratOf("1/3")},
			b:    ratInt64s(1, 0, 0),
			want: "1",
		},
		{
			name: "non-trivial 4 positions",
			a:    []*big.Rat{ratOf("1/4"), ratOf("1/4"), ratOf("1/4"), ratOf("1/4")},
			b:    []*big.Rat{ratOf("1/2"), ratOf("0"), ratOf("0"), ratOf("1/2")},
			want: "1/2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Wasserstein1(tc.a, tc.b)
			if err != nil {
				t.Fatalf("Wasserstein1 error: %v", err)
			}
			if got.Cmp(ratOf(tc.want)) != 0 {
				t.Fatalf("distance = %s, want %s", got.RatString(), tc.want)
			}
			flows, err := TransportFlows(tc.a, tc.b)
			if err != nil {
				t.Fatalf("TransportFlows error: %v", err)
			}
			sum := new(big.Rat)
			for _, f := range flows {
				sum.Add(sum, f.Cost())
			}
			if sum.Cmp(got) != 0 {
				t.Fatalf("flow cost sum %s != distance %s", sum.RatString(), got.RatString())
			}
		})
	}
}

func TestWassersteinEndpointsAndZeroSample(t *testing.T) {
	for k := 1; k <= 6; k++ {
		left := make([]*big.Rat, k)
		right := make([]*big.Rat, k)
		for i := range left {
			left[i] = new(big.Rat)
			right[i] = new(big.Rat)
		}
		left[0] = big.NewRat(1, 1)
		right[k-1] = big.NewRat(1, 1)

		got, err := Wasserstein1(left, right)
		if err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		want := big.NewRat(int64(k-1), 1)
		if got.Cmp(want) != 0 {
			t.Fatalf("k=%d endpoint distance = %s, want %s", k, got.RatString(), want.RatString())
		}

		zero := make([]*big.Rat, k)
		if _, err := Wasserstein1(zero, left); err == nil {
			t.Fatalf("k=%d: zero-sample distribution must be rejected", k)
		}
	}
}

func TestWassersteinExactNotFloat(t *testing.T) {
	// Counts 101 vs 103 produce denominators that cannot be represented
	// exactly in binary floating point; the rational result must be exact.
	groupCounts := []int64{101, 0}
	popCounts := []int64{0, 103}
	got, err := CountWasserstein1(groupCounts, 101, popCounts, 103)
	if err != nil {
		t.Fatalf("CountWasserstein1 error: %v", err)
	}
	want := ratOf("1")
	if got.Cmp(want) != 0 {
		t.Fatalf("distance = %s, want 1", got.RatString())
	}

	groupCounts = []int64{1, 2}
	popCounts = []int64{2, 1}
	got, err = CountWasserstein1(groupCounts, 3, popCounts, 3)
	if err != nil {
		t.Fatalf("CountWasserstein1 error: %v", err)
	}
	if got.Cmp(ratOf("1/3")) != 0 {
		t.Fatalf("distance = %s, want 1/3", got.RatString())
	}
}

// compositions enumerates every length-k non-negative integer vector summing
// to total into out, via recursive weak-composition generation.
func compositions(k, total int64, out *[][]int64) {
	var rec func(pos, remaining int64, prefix []int64)
	rec = func(pos, remaining int64, prefix []int64) {
		if pos == k-1 {
			vector := make([]int64, k)
			copy(vector, prefix)
			vector[pos] = remaining
			*out = append(*out, vector)
			return
		}
		for v := int64(0); v <= remaining; v++ {
			rec(pos+1, remaining-v, append(prefix, v))
		}
	}
	rec(0, total, nil)
}

// referenceCountEMD is an independent, explicitly greedy transport simulation
// over unit atoms: at each boundary move the exact surplus/deficit across,
// accumulating cost with rational arithmetic.
func referenceCountEMD(groupCounts []int64, groupTotal int64, popCounts []int64, popTotal int64) *big.Rat {
	k := len(groupCounts)
	carry := big.NewRat(0, 1)
	cost := big.NewRat(0, 1)
	gDenom := big.NewInt(groupTotal)
	pDenom := big.NewInt(popTotal)
	for i := 0; i+1 < k; i++ {
		gProb := new(big.Rat).SetFrac(big.NewInt(groupCounts[i]), new(big.Int).Set(gDenom))
		pProb := new(big.Rat).SetFrac(big.NewInt(popCounts[i]), new(big.Int).Set(pDenom))
		carry.Add(carry, new(big.Rat).Sub(gProb, pProb))
		cost.Add(cost, new(big.Rat).Abs(carry))
	}
	return cost
}

func TestExhaustiveSmallDistributions(t *testing.T) {
	const maxTotal = 5
	for k := 1; k <= 4; k++ {
		var groupVectors, popVectors [][]int64
		for total := int64(1); total <= maxTotal; total++ {
			compositions(int64(k), total, &groupVectors)
			compositions(int64(k), total, &popVectors)
		}

		for _, g := range groupVectors {
			gTotal := int64(0)
			for _, c := range g {
				gTotal += c
			}
			for _, p := range popVectors {
				pTotal := int64(0)
				for _, c := range p {
					pTotal += c
				}

				got, err := CountWasserstein1(g, gTotal, p, pTotal)
				if err != nil {
					t.Fatalf("k=%d g=%v p=%v: unexpected error %v", k, g, p, err)
				}
				want := referenceCountEMD(g, gTotal, p, pTotal)
				if got.Cmp(want) != 0 {
					t.Fatalf("k=%d g=%v p=%v: distance %s != reference %s",
						k, g, p, got.RatString(), want.RatString())
				}

				gDist := distFromCounts(g, gTotal)
				pDist := distFromCounts(p, pTotal)
				if sum := ratSum(gDist); sum.Cmp(big.NewRat(1, 1)) != 0 {
					t.Fatalf("group distribution sums to %s, want 1", sum.RatString())
				}
				if sum := ratSum(pDist); sum.Cmp(big.NewRat(1, 1)) != 0 {
					t.Fatalf("population distribution sums to %s, want 1", sum.RatString())
				}

				distDistance, err := Wasserstein1(gDist, pDist)
				if err != nil {
					t.Fatalf("Wasserstein1 error: %v", err)
				}
				if distDistance.Cmp(got) != 0 {
					t.Fatalf("count distance %s != distribution distance %s",
						got.RatString(), distDistance.RatString())
				}

				flows, err := TransportFlows(gDist, pDist)
				if err != nil {
					t.Fatalf("TransportFlows error: %v", err)
				}
				flowCost := big.NewRat(0, 1)
				for _, f := range flows {
					flowCost.Add(flowCost, f.Cost())
				}
				if flowCost.Cmp(got) != 0 {
					t.Fatalf("flow evidence cost %s != distance %s", flowCost.RatString(), got.RatString())
				}
			}
		}

		// Endpoint vectors over every enumerated total must be exactly k-1 apart.
		left := make([]int64, k)
		right := make([]int64, k)
		left[0] = maxTotal
		right[k-1] = maxTotal
		got, _ := CountWasserstein1(left, maxTotal, right, maxTotal)
		if got.Cmp(big.NewRat(int64(k-1), 1)) != 0 {
			t.Fatalf("k=%d endpoint distance = %s, want %d", k, got.RatString(), k-1)
		}
	}
}

func ratSum(dist []*big.Rat) *big.Rat {
	sum := new(big.Rat)
	for _, p := range dist {
		sum.Add(sum, p)
	}
	return sum
}

func TestWassersteinRejectsBadInputs(t *testing.T) {
	if _, err := Wasserstein1(ratInt64s(1, 0), ratInt64s(1, 0, 0)); err == nil {
		t.Fatal("expected length mismatch error")
	}
	if _, err := CountWasserstein1([]int64{1, 0}, 1, []int64{0, 1}, 0); err == nil {
		t.Fatal("expected non-positive population error")
	}
	if _, err := CountWasserstein1([]int64{0, 0}, 0, []int64{0, 1}, 1); err == nil {
		t.Fatal("expected non-positive group error")
	}
	if _, err := TransportFlows([]*big.Rat{nil, big.NewRat(1, 1)}, ratInt64s(0, 1)); err == nil {
		t.Fatal("expected nil probability error")
	}
}
