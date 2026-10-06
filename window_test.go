package windowagg

import (
	"math"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

// checkRat asserts got equals want and is a fully reduced rational.
func checkRat(t *testing.T, got, want *big.Rat, wantOK bool, what string) {
	t.Helper()
	if got.Cmp(want) != 0 {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
	// big.Rat is always normalized; assert the denominator is positive
	// and the fraction is fully reduced.
	if got.Denom().Sign() <= 0 {
		t.Fatalf("%s: non-positive denominator in %v", what, got)
	}
	num := new(big.Int).Abs(got.Num())
	g := new(big.Int).GCD(nil, nil, num, got.Denom())
	if num.Sign() != 0 && g.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("%s: %v is not reduced", what, got)
	}
}

func TestInsertRemoveDuplicatesCountedIndividually(t *testing.T) {
	w := New()
	w.Insert(5)
	w.Insert(5)
	w.Insert(5)
	w.Insert(2)
	if w.Len() != 4 {
		t.Fatalf("Len=%d, want 4", w.Len())
	}
	if !w.Remove(5) {
		t.Fatal("Remove(5) should succeed")
	}
	if w.Len() != 3 {
		t.Fatalf("Len=%d after one remove, want 3", w.Len())
	}
	checkRat(t, mustMed(t, w), rat("5"), true, "median of {2,5,5}")
	if !w.Remove(5) || !w.Remove(5) {
		t.Fatal("remaining two 5s should be removable")
	}
	if w.Remove(5) {
		t.Fatal("Remove(5) should fail once all occurrences are gone")
	}
	if w.Remove(99) {
		t.Fatal("Remove of absent value should fail")
	}
	checkRat(t, mustMed(t, w), rat("2"), true, "median of {2}")
}

func mustMed(t *testing.T, w *Window) *big.Rat {
	t.Helper()
	m, ok := w.Median()
	if !ok {
		t.Fatal("median unexpectedly missing")
	}
	return m
}

func TestEmptyWindow(t *testing.T) {
	w := New()
	if m, ok := w.Median(); ok || m != nil {
		t.Fatalf("empty median = %v,%v want nil,false", m, ok)
	}
	for _, k := range []int{0, 1, 3} {
		if m, ok := w.TrimmedMean(k); ok || m != nil {
			t.Fatalf("empty trimmed mean k=%d = %v,%v want nil,false", k, m, ok)
		}
	}
	if got := w.Sum(); got.Sign() != 0 {
		t.Fatalf("empty sum = %v, want 0", got)
	}
	// Window drained back to empty behaves like a fresh one.
	w.Insert(7)
	w.Remove(7)
	if _, ok := w.Median(); ok {
		t.Fatal("median should be missing after draining the window")
	}
}

func TestMedianExact(t *testing.T) {
	w := New()
	for _, v := range []int64{4, 1, 3} {
		w.Insert(v)
	}
	checkRat(t, mustMed(t, w), rat("3"), true, "median of {1,3,4}")
	w.Insert(10) // {1,3,4,10}
	checkRat(t, mustMed(t, w), rat("7/2"), true, "median of {1,3,4,10}")
	w.Remove(1) // {3,4,10}
	checkRat(t, mustMed(t, w), rat("4"), true, "median of {3,4,10}")
}

func TestTrimmedMeanDuplicateBoundaries(t *testing.T) {
	w := New()
	for _, v := range []int64{1, 1, 1, 5, 9, 9, 9} {
		w.Insert(v)
	}
	// k=1 drops one 1 and one 9 -> {1,1,5,9,9}, sum 25.
	checkRat(t, mustTrim(t, w, 1), rat("5"), true, "k=1")
	// k=2 drops two 1s and two 9s -> {1,5,9}, sum 15.
	checkRat(t, mustTrim(t, w, 2), rat("5"), true, "k=2")
	// k=3 leaves exactly 2k+1 = 7 values' middle one -> {5}.
	checkRat(t, mustTrim(t, w, 3), rat("5"), true, "k=3")
	// k=4 needs 9 values, only 7 present.
	if _, ok := w.TrimmedMean(4); ok {
		t.Fatal("k=4 with 7 values should be missing")
	}
	// Non-reducible-looking sums must come back reduced: {2,2,4,4,6}, k=1 -> {2,4,4}, 10/3.
	w2 := New()
	for _, v := range []int64{2, 2, 4, 4, 6} {
		w2.Insert(v)
	}
	checkRat(t, mustTrim(t, w2, 1), rat("10/3"), true, "k=1 of {2,2,4,4,6}")
	// Duplicates straddling the trim cut: {3,3,3,3}, k=0 -> 3.
	w3 := New()
	for i := 0; i < 4; i++ {
		w3.Insert(3)
	}
	checkRat(t, mustTrim(t, w3, 0), rat("3"), true, "k=0 of four 3s")
}

func mustTrim(t *testing.T, w *Window, k int) *big.Rat {
	t.Helper()
	m, ok := w.TrimmedMean(k)
	if !ok {
		t.Fatalf("trimmed mean k=%d unexpectedly missing", k)
	}
	return m
}

func TestTrimmedMeanCountBoundary(t *testing.T) {
	w := New()
	for _, v := range []int64{1, 2, 3, 4} {
		w.Insert(v)
	}
	// n=4: k=1 needs 3 values -> ok ({2,3} -> 5/2); k=2 needs 5 -> missing.
	checkRat(t, mustTrim(t, w, 1), rat("5/2"), true, "n=4 k=1")
	if _, ok := w.TrimmedMean(2); ok {
		t.Fatal("n=4 k=2 (n == 2k) should be missing")
	}
	if _, ok := w.TrimmedMean(-1); ok {
		t.Fatal("negative k should be missing")
	}
	// n=2k exactly is missing; removing down to 2k from 2k+1 flips ok.
	w.Remove(4) // n=3, k=1 -> {2} -> 2
	checkRat(t, mustTrim(t, w, 1), rat("2"), true, "n=3 k=1")
	w.Remove(3) // n=2 == 2k
	if _, ok := w.TrimmedMean(1); ok {
		t.Fatal("n=2 k=1 should be missing")
	}
}

func TestBigIntegerSums(t *testing.T) {
	w := New()
	max := int64(math.MaxInt64)
	min := int64(math.MinInt64)
	// Sum overflows int64 many times over; must stay exact.
	for i := 0; i < 100; i++ {
		w.Insert(max)
	}
	wantSum := new(big.Int).Mul(big.NewInt(max), big.NewInt(100))
	if got := w.Sum(); got.Cmp(wantSum) != 0 {
		t.Fatalf("Sum=%v, want %v", got, wantSum)
	}
	checkRat(t, mustTrim(t, w, 10), rat("9223372036854775807"), true, "trimmed mean of 100 MaxInt64")
	checkRat(t, mustMed(t, w), rat("9223372036854775807"), true, "median of MaxInt64s")

	// Mixed extremes: even count median of {MinInt64, MaxInt64} = -1/2.
	w2 := New()
	w2.Insert(min)
	w2.Insert(max)
	checkRat(t, mustMed(t, w2), rat("-1/2"), true, "median of {MinInt64,MaxInt64}")
	// {MinInt64, 0, MaxInt64}: trimmed mean k=1 -> 0, sum must not overflow.
	w2.Insert(0)
	checkRat(t, mustTrim(t, w2, 1), rat("0"), true, "trimmed mean of {Min,0,Max}")
	if got := w2.Sum(); got.Cmp(big.NewInt(-1)) != 0 {
		t.Fatalf("Sum=%v, want -1", got)
	}
	// Removing a huge value updates the sum exactly.
	w2.Remove(min)
	if got := w2.Sum(); got.Cmp(big.NewInt(max)) != 0 {
		t.Fatalf("Sum after remove=%v, want %v", got, max)
	}
}

// naive is a brute-force reference model: an unsorted slice, sorted on demand.
type naive []int64

func (n *naive) insert(v int64) { *n = append(*n, v) }

func (n *naive) remove(v int64) bool {
	for i, x := range *n {
		if x == v {
			*n = append((*n)[:i], (*n)[i+1:]...)
			return true
		}
	}
	return false
}

func (n naive) sorted() []int64 {
	s := append([]int64(nil), n...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s
}

func (n naive) median() (*big.Rat, bool) {
	s := n.sorted()
	if len(s) == 0 {
		return nil, false
	}
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return new(big.Rat).SetInt64(s[mid]), true
	}
	num := new(big.Int).Add(big.NewInt(s[mid-1]), big.NewInt(s[mid]))
	return new(big.Rat).SetFrac(num, big.NewInt(2)), true
}

func (n naive) trimmedMean(k int) (*big.Rat, bool) {
	s := n.sorted()
	if len(s) < 2*k+1 {
		return nil, false
	}
	sum := new(big.Int)
	for _, v := range s[k : len(s)-k] {
		sum.Add(sum, big.NewInt(v))
	}
	return new(big.Rat).SetFrac(sum, big.NewInt(int64(len(s)-2*k))), true
}

func TestAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	w := New()
	var model naive
	// Small value pool forces many duplicates at the trim boundaries.
	pool := []int64{-2, -1, 0, 1, 2, math.MaxInt64, math.MinInt64}
	for step := 0; step < 5000; step++ {
		v := pool[rng.Intn(len(pool))]
		if rng.Intn(2) == 0 || len(model) == 0 {
			w.Insert(v)
			model.insert(v)
		} else {
			if got, want := w.Remove(v), model.remove(v); got != want {
				t.Fatalf("step %d: Remove(%d)=%v, model=%v", step, v, got, want)
			}
		}
		if w.Len() != len(model) {
			t.Fatalf("step %d: Len=%d, model=%d", step, w.Len(), len(model))
		}
		gm, gok := w.Median()
		wm, wok := model.median()
		if gok != wok || (gok && gm.Cmp(wm) != 0) {
			t.Fatalf("step %d: median=%v,%v model=%v,%v", step, gm, gok, wm, wok)
		}
		for k := 0; k <= 4; k++ {
			gt, gok := w.TrimmedMean(k)
			wt, wok := model.trimmedMean(k)
			if gok != wok || (gok && gt.Cmp(wt) != 0) {
				t.Fatalf("step %d: trimmed k=%d = %v,%v model=%v,%v", step, k, gt, gok, wt, wok)
			}
		}
		sum := new(big.Int)
		for _, v := range model {
			sum.Add(sum, big.NewInt(v))
		}
		if w.Sum().Cmp(sum) != 0 {
			t.Fatalf("step %d: Sum=%v, model=%v", step, w.Sum(), sum)
		}
	}
}
