package streamsum

import (
	"math"
	"math/big"
	"testing"
)

// exactSum recomputes the exact sum of binary64 values as a rational
// number. big.Rat.SetFloat64 is exact for finite float64 values because
// every binary floating-point value is a dyadic rational.
func exactSum(xs []float64) *big.Rat {
	total := new(big.Rat)
	tmp := new(big.Rat)
	for _, x := range xs {
		tmp.SetFloat64(x)
		if tmp == nil {
			panic("non-finite value in exactSum")
		}
		total.Add(total, tmp)
	}
	return total
}

// errOf returns |computed - exact(xs)| as a float64.
func errOf(computed float64, xs []float64) float64 {
	diff := new(big.Rat).Sub(new(big.Rat).SetFloat64(computed), exactSum(xs))
	abs := new(big.Rat).Abs(diff)
	f, _ := abs.Float64()
	return f
}

func concat(groups ...[]float64) []float64 {
	var out []float64
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func checkBound(t *testing.T, label string, got, bound float64, xs []float64) {
	t.Helper()
	if math.IsInf(bound, 0) || math.IsNaN(bound) {
		t.Fatalf("%s: expected finite bound for finite data, got %v", label, bound)
	}
	if bound < 0 {
		t.Fatalf("%s: negative bound %v", label, bound)
	}
	if e := errOf(got, xs); e > bound {
		t.Fatalf("%s: bound violated: |err|=%g > bound=%g", label, e, bound)
	}
}

// Small example recomputed exactly with rationals.
func TestExactSmallExample(t *testing.T) {
	xs := []float64{0.1, 0.2, 0.3, 1e-4, -5.5, 1.25, 3.75e2}
	a := NewAccumulator()
	a.AddChunk(xs)
	got, bound := a.Total()
	checkBound(t, "small example", got, bound, xs)

	// The compensated result must be at least as accurate as naive summation.
	naive := 0.0
	for _, x := range xs {
		naive += x
	}
	if errOf(got, xs) > errOf(naive, xs) {
		t.Fatalf("compensated error %g exceeds naive error %g", errOf(got, xs), errOf(naive, xs))
	}
}

// Catastrophic cancellation: 1e16 + 1 - 1e16 must keep the 1.
func TestCancellation(t *testing.T) {
	xs := []float64{1e16, 1, -1e16}
	a := NewAccumulator()
	a.AddChunk(xs)
	got, bound := a.Total()
	if got != 1 {
		t.Fatalf("cancellation: got %v, want exactly 1", got)
	}
	checkBound(t, "cancellation", got, bound, xs)

	big := 1e16 // runtime variable: prevents compile-time constant folding
	naive := (big + 1) - big
	if naive != 0 {
		t.Fatalf("sanity: expected naive sum to lose the 1, got %v", naive)
	}
}

// Small increments below half the ulp of the running total are lost by
// naive summation but retained by compensation.
func TestSmallIncrementLoss(t *testing.T) {
	const increments = 1000
	xs := make([]float64, 0, increments+1)
	xs = append(xs, 1)
	for i := 0; i < increments; i++ {
		xs = append(xs, 1e-17) // < ulp(1)/2 ~ 1.11e-16
	}
	a := NewAccumulator()
	a.AddChunk(xs)
	got, bound := a.Total()
	checkBound(t, "small increments", got, bound, xs)

	exact, _ := exactSum(xs).Float64()
	if math.Abs(got-exact) > 1e-19 {
		t.Fatalf("compensated sum %v drifted from exact %v", got, exact)
	}
	naive := 0.0
	for _, x := range xs {
		naive += x
	}
	if naive != 1 {
		t.Fatalf("sanity: expected naive sum to lose all increments, got %v", naive)
	}
}

// Different chunk sizes change the error bound; every configuration must
// still bound the true error.
func TestChunkSizesAffectBound(t *testing.T) {
	base := []float64{1e8, 3.25, -7.5e7, 0.125, 4e6, -2.5, 9.75e3, -1e8, 42, 5e5, -0.5, 7.75e6}
	all := concat(base, base, base) // 36 values

	configs := []struct {
		label string
		chunk int
	}{
		{"one chunk of 36", 36},
		{"chunks of 6", 6},
		{"chunks of 3", 3},
		{"chunks of 1", 1},
	}
	bounds := map[string]float64{}
	for _, cfg := range configs {
		// Unmerged: Total folds the chunk stack.
		a := NewAccumulator()
		for i := 0; i < len(all); i += cfg.chunk {
			a.AddChunk(all[i : i+cfg.chunk])
		}
		got, bound := a.Total()
		checkBound(t, cfg.label+" unmerged", got, bound, all)
		bounds[cfg.label+" unmerged"] = bound

		// Fully merged into a single summary.
		b := NewAccumulator()
		for i := 0; i < len(all); i += cfg.chunk {
			b.AddChunk(all[i : i+cfg.chunk])
		}
		for b.Chunks() > 1 {
			b.Merge()
		}
		gotM, boundM := b.Total()
		checkBound(t, cfg.label+" merged", gotM, boundM, all)
		bounds[cfg.label+" merged"] = boundM
	}

	// Merging adds rounding steps, so the fully merged per-element
	// configuration must carry a larger bound than the single chunk.
	if bounds["chunks of 1 merged"] <= bounds["one chunk of 36 merged"] {
		t.Fatalf("expected per-element merged bound %g > single-chunk bound %g",
			bounds["chunks of 1 merged"], bounds["one chunk of 36 merged"])
	}
	for label, b := range bounds {
		t.Logf("%-28s bound=%g", label, b)
	}
}

// Retract removes the most recent chunk's contribution exactly.
func TestRetract(t *testing.T) {
	c1 := []float64{1.5, -2.25, 1e10}
	c2 := []float64{1e-9, 1e-9}
	c3 := []float64{-1e10, 7}
	a := NewAccumulator()
	a.AddChunk(c1)
	a.AddChunk(c2)
	a.AddChunk(c3)

	info, ok := a.Retract()
	if !ok || info.Count != 2 {
		t.Fatalf("retract: got %+v ok=%v", info, ok)
	}
	got, bound := a.Total()
	checkBound(t, "after retract", got, bound, concat(c1, c2))

	info, ok = a.Retract()
	if !ok || info.Count != 2 {
		t.Fatalf("retract: got %+v ok=%v", info, ok)
	}
	got, bound = a.Total()
	checkBound(t, "after two retracts", got, bound, c1)

	if _, ok = a.Retract(); !ok {
		t.Fatal("expected third retract to succeed")
	}
	if _, ok = a.Retract(); ok {
		t.Fatal("expected retract on empty accumulator to fail")
	}
	if got, bound := a.Total(); got != 0 || bound != 0 {
		t.Fatalf("empty total: got (%v, %v)", got, bound)
	}
}

// Merged chunks retract together as one unit.
func TestMergeThenRetract(t *testing.T) {
	c1 := []float64{1e12, 1, -1e12}
	c2 := []float64{5}
	a := NewAccumulator()
	a.AddChunk(c1)
	a.AddChunk(c2)
	a.Merge()
	if a.Chunks() != 1 {
		t.Fatalf("expected 1 chunk after merge, got %d", a.Chunks())
	}
	got, bound := a.Total()
	checkBound(t, "merged total", got, bound, concat(c1, c2))
	if got != 6 {
		t.Fatalf("merged total: got %v, want 6", got)
	}
	if _, ok := a.Retract(); !ok {
		t.Fatal("retract of merged chunk failed")
	}
	if got, _ := a.Total(); got != 0 {
		t.Fatalf("expected 0 after retracting merged chunk, got %v", got)
	}
}

// Public non-finite rules: results follow IEEE 754, bounds are +Inf.
func TestNonFiniteRules(t *testing.T) {
	cases := []struct {
		label string
		xs    []float64
		want  float64
	}{
		{"plus inf", []float64{1, math.Inf(1)}, math.Inf(1)},
		{"minus inf", []float64{math.Inf(-1), 2}, math.Inf(-1)},
		{"inf minus inf", []float64{math.Inf(1), math.Inf(-1)}, math.NaN()},
		{"nan input", []float64{1, math.NaN()}, math.NaN()},
		{"overflow", []float64{math.MaxFloat64, math.MaxFloat64}, math.Inf(1)},
	}
	for _, tc := range cases {
		a := NewAccumulator()
		a.AddChunk([]float64{1})
		a.AddChunk(tc.xs)
		got, bound := a.Total()
		same := got == tc.want || (math.IsNaN(tc.want) && math.IsNaN(got))
		if !same {
			t.Fatalf("%s: got %v, want %v", tc.label, got, tc.want)
		}
		if !math.IsInf(bound, 1) {
			t.Fatalf("%s: bound must be +Inf for non-finite arithmetic, got %v", tc.label, bound)
		}
	}
}

// Retracting the chunk that introduced a non-finite value restores a
// finite result and a finite bound.
func TestRetractNonFiniteChunk(t *testing.T) {
	good := []float64{3.5, -1.25}
	a := NewAccumulator()
	a.AddChunk(good)
	a.AddChunk([]float64{math.Inf(1)})
	if got, bound := a.Total(); !math.IsInf(got, 1) || !math.IsInf(bound, 1) {
		t.Fatalf("with inf chunk: got (%v, %v)", got, bound)
	}
	if _, ok := a.Retract(); !ok {
		t.Fatal("retract failed")
	}
	got, bound := a.Total()
	checkBound(t, "after retracting inf chunk", got, bound, good)
	if got != 2.25 {
		t.Fatalf("got %v, want 2.25", got)
	}
}
