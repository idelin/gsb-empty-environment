package permtest

import (
	"math"
	"math/bits"
	"strings"
	"testing"
)

// bruteForceTwoSample is an independent implementation: bitmask enumeration
// with float64 arithmetic, used only to cross-check TwoSample.
func bruteForceTwoSample(x, y []float64) (tail, total int) {
	n1, n2 := len(x), len(y)
	n := n1 + n2
	all := append(append([]float64{}, x...), y...)
	sum := 0.0
	for _, v := range all {
		sum += v
	}
	mean := func(s float64, k int) float64 { return s / float64(k) }
	obs := 0.0
	for _, v := range x {
		obs += v
	}
	obs = math.Abs(mean(obs, n1) - mean(sum-obs, n2))
	for mask := 0; mask < 1<<uint(n); mask++ {
		if bits.OnesCount(uint(mask)) != n1 {
			continue
		}
		sa := 0.0
		for i := 0; i < n; i++ {
			if mask>>uint(i)&1 == 1 {
				sa += all[i]
			}
		}
		d := math.Abs(mean(sa, n1) - mean(sum-sa, n2))
		if d >= obs-1e-9 {
			tail++
		}
		total++
	}
	return tail, total
}

func TestTwoSampleDuplicatesByPosition(t *testing.T) {
	// Values [1,1,1,2] pooled; deduplicating values would shrink the sample
	// space, but positions give C(4,2) = 6 assignments, all in the tail.
	f, err := TwoSample([]float64{1, 1}, []float64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if f.Num != 6 || f.Den != 6 {
		t.Fatalf("got %v, want 6/6", f)
	}
	if r := f.Reduced(); r.Num != 1 || r.Den != 1 {
		t.Fatalf("reduced = %v, want 1/1", r)
	}
}

func TestTwoSampleNegativeValues(t *testing.T) {
	// obs diff = -1.5 - 3.5 = -5; only the two extreme splits reach |d| >= 5.
	f, err := TwoSample([]float64{-1, -2}, []float64{3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if f.Num != 2 || f.Den != 6 {
		t.Fatalf("got %v, want 2/6", f)
	}
	if r := f.Reduced(); r.Num != 1 || r.Den != 3 {
		t.Fatalf("reduced = %v, want 1/3", r)
	}
}

func TestTwoSampleUnequalSizes(t *testing.T) {
	// obs diff = 6 - 1.5 = 4.5; scaled threshold |sa*2 - sb*3| >= 27, met
	// only by the observed split itself.
	f, err := TwoSample([]float64{5, 6, 7}, []float64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if f.Num != 1 || f.Den != 10 {
		t.Fatalf("got %v, want 1/10", f)
	}
}

func TestTwoSampleAgainstBruteForce(t *testing.T) {
	// Deterministic pseudo-random cases with duplicate values, negative
	// values and unequal group sizes.
	seed := uint32(12345)
	next := func(mod int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>16) % mod
	}
	for tc := 0; tc < 200; tc++ {
		n1 := 1 + next(5)
		n2 := 1 + next(5)
		x := make([]float64, n1)
		y := make([]float64, n2)
		for i := range x {
			x[i] = float64(next(7) - 3) // duplicates and negatives guaranteed
		}
		for i := range y {
			y[i] = float64(next(7) - 3)
		}
		got, err := TwoSample(x, y)
		if err != nil {
			t.Fatal(err)
		}
		wt, wn := bruteForceTwoSample(x, y)
		if got.Num != int64(wt) || got.Den != int64(wn) {
			t.Fatalf("x=%v y=%v: got %v, brute force %d/%d", x, y, got, wt, wn)
		}
	}
}

func TestTwoSampleErrors(t *testing.T) {
	if _, err := TwoSample(nil, []float64{1}); err == nil {
		t.Fatal("empty group: want error")
	}
	big := make([]float64, MaxGroupSize+1)
	if _, err := TwoSample(big, []float64{1}); err == nil {
		t.Fatal("oversized group: want error")
	}
	if _, err := TwoSample([]float64{math.NaN()}, []float64{1}); err == nil {
		t.Fatal("NaN: want error")
	}
}

func TestFractionString(t *testing.T) {
	s := Fraction{Num: 2, Den: 6}.String()
	if !strings.Contains(s, "2/6") || !strings.Contains(s, "1/3") {
		t.Fatalf("String() = %q, want numerator/denominator and reduced form", s)
	}
}
