package streamsum

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// exactSum recomputes the sum of finite float64 values exactly, using the
// exact rational value of each binary floating-point number.
func exactSum(xs []float64) *big.Rat {
	acc := new(big.Rat)
	for _, x := range xs {
		r := new(big.Rat).SetFloat64(x)
		if r == nil {
			panic("exactSum: non-finite input")
		}
		acc.Add(acc, r)
	}
	return acc
}

// absDiffRat returns |f - exact| as an exact rational.
func absDiffRat(f float64, exact *big.Rat) *big.Rat {
	r := new(big.Rat).SetFloat64(f)
	if r == nil {
		panic("absDiffRat: non-finite float")
	}
	d := new(big.Rat).Sub(r, exact)
	return d.Abs(d)
}

// ratOf converts a finite float64 to its exact rational value.
func ratOf(f float64) *big.Rat {
	r := new(big.Rat).SetFloat64(f)
	if r == nil {
		panic("ratOf: non-finite float")
	}
	return r
}

// checkBound asserts |Result - exact| <= ErrorBound and returns the error.
func checkBound(t *testing.T, s *Summer, exact *big.Rat) *big.Rat {
	t.Helper()
	res := s.Result()
	bnd := s.ErrorBound()
	if math.IsNaN(res) || math.IsInf(res, 0) {
		t.Fatalf("Result = %v, want finite", res)
	}
	if math.IsInf(bnd, 0) || math.IsNaN(bnd) || bnd < 0 {
		t.Fatalf("ErrorBound = %v, want finite non-negative", bnd)
	}
	diff := absDiffRat(res, exact)
	if diff.Cmp(ratOf(bnd)) > 0 {
		t.Fatalf("error bound violated: |%v - exact| = %v > bound %v",
			res, diff.FloatString(30), bnd)
	}
	return diff
}

func TestSmallExampleExactRational(t *testing.T) {
	// Values with inexact decimal representations; the exact rational
	// recomputation uses their true binary values.
	xs := []float64{0.1, 0.2, 0.3, -0.15, 1.5, 2.25, -0.75, 1e-3}
	s := New()
	s.AddBlock(xs)
	got := s.Result()
	exact := exactSum(xs)
	diff := absDiffRat(got, exact)
	t.Logf("result=%.17g exact=%s |diff|=%s bound=%g",
		got, exact.FloatString(20), diff.FloatString(25), s.ErrorBound())
	checkBound(t, s, exact)
}

func TestLargeCancellation(t *testing.T) {
	// 1e16 + 1 + 1 - 1e16: the 1s are lost in plain float64 addition.
	xs := []float64{1e16, 1, 1, -1e16}
	exact := exactSum(xs) // exactly 2

	// One big block: block-internal naive sum loses the 1; the bound
	// must honestly cover the error.
	big := New()
	big.AddBlock(xs)
	if got := big.Result(); got != 0 {
		t.Fatalf("single-block result = %v, want 0 (cancellation)", got)
	}
	checkBound(t, big, exact)
	if big.ErrorBound() < 1 {
		t.Fatalf("single-block bound %g should cover the lost 1", big.ErrorBound())
	}

	// Four single-element blocks: Neumaier recovers the 2 exactly.
	small := New()
	for _, x := range xs {
		small.Add(x)
	}
	if got := small.Result(); got != 2 {
		t.Fatalf("per-element result = %v, want exact 2", got)
	}
	checkBound(t, small, exact)
	if small.ErrorBound() >= big.ErrorBound() {
		t.Fatalf("per-element bound %g should be tighter than single-block bound %g",
			small.ErrorBound(), big.ErrorBound())
	}
}

func TestSmallIncrementLoss(t *testing.T) {
	// Increments far below the ulp of the running total are lost.
	const k = 1000
	xs := make([]float64, 0, k+1)
	xs = append(xs, 1.0)
	for i := 0; i < k; i++ {
		xs = append(xs, 1e-17)
	}
	exact := exactSum(xs)

	s := New()
	s.AddBlock(xs)
	checkBound(t, s, exact)

	// The plain block sum must actually have lost increments, and the
	// exact error must be nonzero but within the bound.
	diff := absDiffRat(s.Result(), exact)
	if diff.Sign() == 0 {
		t.Fatalf("expected some increment loss, got exact result")
	}
	t.Logf("lost = %s, bound = %g", diff.FloatString(25), s.ErrorBound())
}

func TestBlockSizeEffectOnBound(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	xs := make([]float64, 256)
	for i := range xs {
		xs[i] = rng.NormFloat64() * 1e8
	}
	exact := exactSum(xs)

	sumBlocks := func(bs int) *Summer {
		s := New()
		for i := 0; i < len(xs); i += bs {
			j := i + bs
			if j > len(xs) {
				j = len(xs)
			}
			s.AddBlock(xs[i:j])
		}
		return s
	}

	prev := -1.0
	for _, bs := range []int{1, 4, 16, 64, 256} {
		s := sumBlocks(bs)
		checkBound(t, s, exact)
		b := s.ErrorBound()
		t.Logf("block size %3d: bound = %g", bs, b)
		if b < prev {
			t.Fatalf("bound shrank with larger blocks: size %d gives %g < %g", bs, b, prev)
		}
		prev = b
	}
	// The largest blocks must give a strictly larger bound.
	if sumBlocks(256).ErrorBound() <= sumBlocks(1).ErrorBound() {
		t.Fatalf("expected block size to affect the bound")
	}
}

func TestSpecialValues(t *testing.T) {
	nan, posInf, negInf := math.NaN(), math.Inf(1), math.Inf(-1)

	// NaN anywhere -> NaN result, infinite bound.
	s := New()
	s.AddBlock([]float64{1, 2, nan})
	if !math.IsNaN(s.Result()) {
		t.Fatalf("NaN input: Result = %v, want NaN", s.Result())
	}
	if !math.IsInf(s.ErrorBound(), 1) {
		t.Fatalf("NaN input: ErrorBound = %v, want +Inf", s.ErrorBound())
	}

	// Single-sign infinities -> that infinity, infinite bound.
	s = New()
	s.AddBlock([]float64{posInf, 1})
	s.AddBlock([]float64{posInf, -5})
	if s.Result() != posInf {
		t.Fatalf("+Inf inputs: Result = %v, want +Inf", s.Result())
	}
	if !math.IsInf(s.ErrorBound(), 1) {
		t.Fatalf("+Inf inputs: ErrorBound = %v, want +Inf (no fake finite bound)", s.ErrorBound())
	}

	// Both signs -> NaN.
	s.AddBlock([]float64{negInf})
	if !math.IsNaN(s.Result()) {
		t.Fatalf("+Inf and -Inf: Result = %v, want NaN", s.Result())
	}

	// -Inf only.
	s = New()
	s.AddBlock([]float64{negInf, 3})
	if s.Result() != negInf {
		t.Fatalf("-Inf input: Result = %v, want -Inf", s.Result())
	}

	// Finite overflow to infinity must not carry a finite bound.
	s = New()
	s.AddBlock([]float64{math.MaxFloat64, math.MaxFloat64})
	if s.Result() != posInf {
		t.Fatalf("overflow: Result = %v, want +Inf", s.Result())
	}
	if !math.IsInf(s.ErrorBound(), 1) {
		t.Fatalf("overflow: ErrorBound = %v, want +Inf", s.ErrorBound())
	}

	// Retracting the infinity restores a finite result and bound.
	s = New()
	s.AddBlock([]float64{1, 2, 3})
	s.AddBlock([]float64{posInf})
	if s.Result() != posInf {
		t.Fatalf("before retract: Result = %v, want +Inf", s.Result())
	}
	if !s.Retract() {
		t.Fatal("retract failed")
	}
	if s.Result() != 6 {
		t.Fatalf("after retract: Result = %v, want 6", s.Result())
	}
	checkBound(t, s, exactSum([]float64{1, 2, 3}))
}

func TestRetract(t *testing.T) {
	blocks := [][]float64{
		{1, 2, 3},
		{0.1, 0.2},
		{-4, 5, -6, 7},
		{1e10, -1e10, 1},
	}
	full := New()
	for _, b := range blocks {
		full.AddBlock(b)
	}

	// Retract the last block: must match a summer built without it.
	if !full.Retract() {
		t.Fatal("retract failed")
	}
	want := New()
	var flat []float64
	for _, b := range blocks[:3] {
		want.AddBlock(b)
		flat = append(flat, b...)
	}
	if full.Result() != want.Result() {
		t.Fatalf("after retract: Result = %v, want %v", full.Result(), want.Result())
	}
	if full.ErrorBound() != want.ErrorBound() {
		t.Fatalf("after retract: ErrorBound = %v, want %v",
			full.ErrorBound(), want.ErrorBound())
	}
	checkBound(t, full, exactSum(flat))

	// Retract everything.
	for full.Retract() {
	}
	if full.Result() != 0 || full.Count() != 0 {
		t.Fatalf("fully retracted: Result = %v Count = %d, want 0, 0",
			full.Result(), full.Count())
	}
	if full.Retract() {
		t.Fatal("retract on empty summer should report false")
	}
}

func TestMerge(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	xs := make([]float64, 500)
	for i := range xs {
		xs[i] = rng.NormFloat64()
	}
	exact := exactSum(xs)

	a, b := New(), New()
	a.AddBlock(xs[:200])
	b.AddBlock(xs[200:])
	a.Merge(b)
	checkBound(t, a, exact)

	// Merged result should be close to a single-summer computation.
	c := New()
	c.AddBlock(xs)
	if diff := math.Abs(a.Result() - c.Result()); diff > a.ErrorBound()+c.ErrorBound() {
		t.Fatalf("merged result differs by %g, bounds %g and %g",
			diff, a.ErrorBound(), c.ErrorBound())
	}

	// Merge is retractable.
	pre := New()
	pre.AddBlock(xs[:200])
	a.Retract()
	if a.Result() != pre.Result() || a.ErrorBound() != pre.ErrorBound() {
		t.Fatalf("retract after merge: got (%v, %g), want (%v, %g)",
			a.Result(), a.ErrorBound(), pre.Result(), pre.ErrorBound())
	}

	// Merging special values propagates the public rules.
	p := New()
	p.Add(math.Inf(1))
	q := New()
	q.Add(math.Inf(-1))
	p.Merge(q)
	if !math.IsNaN(p.Result()) || !math.IsInf(p.ErrorBound(), 1) {
		t.Fatalf("merged opposite infinities: Result = %v, bound = %v",
			p.Result(), p.ErrorBound())
	}
}

func TestSingleElementBlocksExact(t *testing.T) {
	// With block size 1 there is no block-internal rounding, so the bound
	// comes only from the global Neumaier term.
	s := New()
	xs := []float64{3.5, -1.25, 2.75, 0.5}
	for _, x := range xs {
		s.Add(x)
	}
	if s.Result() != 5.5 {
		t.Fatalf("Result = %v, want 5.5", s.Result())
	}
	checkBound(t, s, exactSum(xs))
}
