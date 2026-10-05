package distconv

import (
	"math/big"
	"testing"
)

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

// enumerate computes the exact sum distribution of the given independent
// variables by enumerating every combination of values.
func enumerate(dists []*Dist) map[int64]*big.Rat {
	acc := map[int64]*big.Rat{0: big.NewRat(1, 1)}
	for _, d := range dists {
		next := make(map[int64]*big.Rat)
		for s, ps := range acc {
			for i, v := range d.vals {
				key := s + v
				p := new(big.Rat).Mul(ps, d.probs[i])
				if old, ok := next[key]; ok {
					old.Add(old, p)
				} else {
					next[key] = p
				}
			}
		}
		acc = next
	}
	return acc
}

func checkAgainstEnumeration(t *testing.T, got *Dist, want map[int64]*big.Rat) {
	t.Helper()
	if got.Size() != len(want) {
		t.Fatalf("support size = %d, want %d; got %s", got.Size(), len(want), got)
	}
	total := new(big.Rat)
	for v, p := range want {
		total.Add(total, p)
		if gp := got.Prob(v); gp.Cmp(p) != 0 {
			t.Errorf("P(X=%d) = %v, want %v", v, gp, p)
		}
	}
	if total.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("enumerated probabilities sum to %v, not 1", total)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New([]Pair{{0, rat(1, 2)}, {1, rat(1, 3)}}); err == nil {
		t.Error("expected error for probabilities summing to 5/6")
	}
	if _, err := New([]Pair{{0, rat(3, 2)}, {1, rat(-1, 2)}}); err == nil {
		t.Error("expected error for negative probability")
	}
	if _, err := New([]Pair{{0, nil}, {1, rat(1, 1)}}); err == nil {
		t.Error("expected error for nil probability")
	}
	if _, err := New(nil); err == nil {
		t.Error("expected error for empty distribution")
	}
	if _, err := New([]Pair{{5, rat(0, 1)}}); err == nil {
		t.Error("expected error when all mass is zero")
	}
}

func TestMergeDuplicatesAndDropZeros(t *testing.T) {
	d, err := New([]Pair{
		{2, rat(1, 4)},
		{-1, rat(1, 2)},
		{2, rat(1, 4)}, // duplicate: merges to 1/2
		{7, rat(0, 1)}, // zero: removed
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Size() != 2 {
		t.Fatalf("size = %d, want 2: %s", d.Size(), d)
	}
	if p := d.Prob(2); p.Cmp(rat(1, 2)) != 0 {
		t.Errorf("P(X=2) = %v, want 1/2", p)
	}
	if p := d.Prob(-1); p.Cmp(rat(1, 2)) != 0 {
		t.Errorf("P(X=-1) = %v, want 1/2", p)
	}
	if p := d.Prob(7); p.Sign() != 0 {
		t.Errorf("P(X=7) = %v, want 0", p)
	}
	if got := d.Values(); got[0] != -1 || got[1] != 2 {
		t.Errorf("values = %v, want [-1 2]", got)
	}
}

// sampleDists returns distributions exercising negative values, uneven
// rational probabilities with distinct denominators, and degenerates.
func sampleDists() []*Dist {
	return []*Dist{
		Must([]Pair{{-3, rat(1, 6)}, {0, rat(1, 3)}, {2, rat(1, 2)}}),
		Must([]Pair{{-1, rat(2, 5)}, {4, rat(3, 5)}}),
		Degenerate(-7),
		Must([]Pair{{-2, rat(1, 4)}, {-1, rat(1, 4)}, {1, rat(1, 4)}, {3, rat(1, 4)}}),
	}
}

func TestConvolveAllAgainstEnumeration(t *testing.T) {
	dists := sampleDists()
	got, err := ConvolveAll(dists)
	if err != nil {
		t.Fatal(err)
	}
	checkAgainstEnumeration(t, got, enumerate(dists))
}

func TestConvolverMatchesEnumeration(t *testing.T) {
	dists := sampleDists()
	c, err := NewConvolver(dists)
	if err != nil {
		t.Fatal(err)
	}
	checkAgainstEnumeration(t, c.Sum(), enumerate(dists))
}

func TestDegenerateShifts(t *testing.T) {
	d := Must([]Pair{{-2, rat(1, 3)}, {5, rat(2, 3)}})
	got, err := ConvolveAll([]*Dist{d, Degenerate(-10), Degenerate(3)})
	if err != nil {
		t.Fatal(err)
	}
	checkAgainstEnumeration(t, got, map[int64]*big.Rat{
		-9: rat(1, 3),
		-2: rat(2, 3),
	})
}

func TestTailsAgainstEnumeration(t *testing.T) {
	dists := sampleDists()
	sum, err := ConvolveAll(dists)
	if err != nil {
		t.Fatal(err)
	}
	enum := enumerate(dists)

	one := rat(1, 1)
	for threshold := sum.Min() - 1; threshold <= sum.Max()+1; threshold++ {
		le, lt, ge, gt := new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)
		for v, p := range enum {
			if v <= threshold {
				le.Add(le, p)
			}
			if v < threshold {
				lt.Add(lt, p)
			}
			if v >= threshold {
				ge.Add(ge, p)
			}
			if v > threshold {
				gt.Add(gt, p)
			}
		}
		if got := sum.CDF(threshold); got.Cmp(le) != 0 {
			t.Errorf("CDF(%d) = %v, want %v", threshold, got, le)
		}
		if got := sum.Less(threshold); got.Cmp(lt) != 0 {
			t.Errorf("Less(%d) = %v, want %v", threshold, got, lt)
		}
		if got := sum.Geq(threshold); got.Cmp(ge) != 0 {
			t.Errorf("Geq(%d) = %v, want %v", threshold, got, ge)
		}
		if got := sum.SF(threshold); got.Cmp(gt) != 0 {
			t.Errorf("SF(%d) = %v, want %v", threshold, got, gt)
		}
		if got := new(big.Rat).Add(sum.CDF(threshold), sum.SF(threshold)); got.Cmp(one) != 0 {
			t.Errorf("CDF(%d)+SF(%d) = %v, want 1", threshold, threshold, got)
		}
	}
}

func TestQuantileOnCumulativeBoundaries(t *testing.T) {
	d := Must([]Pair{
		{-5, rat(1, 8)},
		{-2, rat(3, 8)}, // cum 1/2
		{0, rat(1, 4)},  // cum 3/4
		{6, rat(1, 4)},  // cum 1
	})
	cases := []struct {
		p    *big.Rat
		want int64
	}{
		{rat(1, 8), -5},  // exactly on first boundary
		{rat(1, 2), -2},  // exactly on internal boundary
		{rat(3, 4), 0},   // exactly on internal boundary
		{rat(1, 1), 6},   // exactly on total
		{rat(1, 16), -5}, // below first boundary
		{rat(9, 16), 0},  // strictly between boundaries 1/2 and 3/4
		{rat(7, 8), 6},   // strictly inside last slab
	}
	for _, tc := range cases {
		got, err := d.Quantile(tc.p)
		if err != nil {
			t.Fatalf("Quantile(%v): %v", tc.p, err)
		}
		if got != tc.want {
			t.Errorf("Quantile(%v) = %d, want %d", tc.p, got, tc.want)
		}
	}
}

func TestQuantileMatchesEnumerationEverywhere(t *testing.T) {
	dists := sampleDists()
	sum, err := ConvolveAll(dists)
	if err != nil {
		t.Fatal(err)
	}
	enum := enumerate(dists)

	// Brute-force quantile: smallest v with cumulative mass >= p.
	brute := func(p *big.Rat) int64 {
		vals := make([]int64, 0, len(enum))
		for v := range enum {
			vals = append(vals, v)
		}
		for i := 0; i < len(vals); i++ {
			for j := i + 1; j < len(vals); j++ {
				if vals[j] < vals[i] {
					vals[i], vals[j] = vals[j], vals[i]
				}
			}
		}
		cum := new(big.Rat)
		for _, v := range vals {
			cum.Add(cum, enum[v])
			if cum.Cmp(p) >= 0 {
				return v
			}
		}
		t.Fatalf("no quantile for %v", p)
		return 0
	}

	// Check every cumulative boundary probability, plus midpoints
	// between consecutive boundaries.
	boundaries := []int64{}
	{
		vals := sum.Values()
		boundaries = append(boundaries, vals...)
	}
	cum := new(big.Rat)
	prev := new(big.Rat)
	for _, v := range boundaries {
		cum.Add(cum, sum.Prob(v))
		for _, p := range []*big.Rat{new(big.Rat).Set(cum), new(big.Rat).Add(prev, cum).Quo(new(big.Rat).Add(prev, cum), rat(2, 1))} {
			got, err := sum.Quantile(p)
			if err != nil {
				t.Fatalf("Quantile(%v): %v", p, err)
			}
			if want := brute(p); got != want {
				t.Errorf("Quantile(%v) = %d, want %d", p, got, want)
			}
		}
		prev.Set(cum)
	}
}

func TestQuantileRejectsInvalidP(t *testing.T) {
	d := Degenerate(0)
	for _, p := range []*big.Rat{nil, rat(0, 1), rat(-1, 2), rat(2, 1)} {
		if _, err := d.Quantile(p); err == nil {
			t.Errorf("expected error for p = %v", p)
		}
	}
}

func TestMean(t *testing.T) {
	d := Must([]Pair{{-3, rat(1, 4)}, {1, rat(1, 2)}, {2, rat(1, 4)}})
	if got := d.Mean(); got.Cmp(rat(1, 4)) != 0 {
		t.Errorf("Mean = %v, want 1/4", got)
	}
}
