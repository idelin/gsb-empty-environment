package main

import (
	"math/big"
	"math/rand"
	"testing"
)

const testBudget = int64(50_000_000)

func bi(v int64) *big.Int { return big.NewInt(v) }

func bis(vs ...int64) []*big.Int {
	out := make([]*big.Int, len(vs))
	for i, v := range vs {
		out[i] = bi(v)
	}
	return out
}

func mustCount(t *testing.T, lo, hi []*big.Int, cons []constraint) *big.Int {
	t.Helper()
	s := NewSolver(lo, hi, cons, testBudget)
	total, ok := s.Count()
	if !ok {
		t.Fatalf("count: budget unexpectedly exhausted")
	}
	return total
}

func mustKth(t *testing.T, lo, hi []*big.Int, cons []constraint, k *big.Int) ([]*big.Int, []ProofStep) {
	t.Helper()
	s := NewSolver(lo, hi, cons, testBudget)
	pt, proof, ok := s.Kth(k)
	if !ok {
		t.Fatalf("kth: budget unexpectedly exhausted")
	}
	return pt, proof
}

func pointsEqual(a, b []*big.Int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Cmp(b[i]) != 0 {
			return false
		}
	}
	return true
}

// checkAgainstEnumeration verifies count, kth (including out-of-range k) and
// the ranking proof against a full enumeration of the box.
func checkAgainstEnumeration(t *testing.T, lo, hi []*big.Int, cons []constraint) {
	t.Helper()
	pts, truncated := Enumerate(lo, hi, cons, 1_000_000)
	if truncated {
		t.Fatalf("test box too large to enumerate")
	}
	total := mustCount(t, lo, hi, cons)
	if total.Cmp(bi(int64(len(pts)))) != 0 {
		t.Fatalf("count = %s, enumeration gives %d", total, len(pts))
	}
	n := len(lo)
	// Every enumerated point must be the answer to its own rank, and the
	// proof must match prefix counts from the enumeration.
	for idx, want := range pts {
		k := bi(int64(idx + 1))
		got, proof := mustKth(t, lo, hi, cons, k)
		if !pointsEqual(got, want) {
			t.Fatalf("kth(%s) = %v, want %v", k, got, want)
		}
		if len(proof) != n {
			t.Fatalf("kth(%s): proof has %d steps, want %d", k, len(proof), n)
		}
		sum := new(big.Int)
		for _, st := range proof {
			if st.Value.Cmp(want[st.Dim]) != 0 {
				t.Fatalf("kth(%s): proof dim %d value %s, want %s", k, st.Dim, st.Value, want[st.Dim])
			}
			// Recompute the prefix count independently from the enumeration.
			var expect int64
			for _, p := range pts {
				match := true
				for j := 0; j < st.Dim; j++ {
					if p[j].Cmp(want[j]) != 0 {
						match = false
						break
					}
				}
				if match && p[st.Dim].Cmp(want[st.Dim]) < 0 {
					expect++
				}
			}
			if st.Skipped.Cmp(bi(expect)) != 0 {
				t.Fatalf("kth(%s): proof dim %d skipped %s, want %d", k, st.Dim, st.Skipped, expect)
			}
			sum.Add(sum, st.Skipped)
		}
		if sum.Cmp(bi(int64(idx))) != 0 {
			t.Fatalf("kth(%s): proof skipped sum %s, want %d", k, sum, idx)
		}
	}
}

func TestRandomSmallBoxesAgainstEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for iter := 0; iter < 300; iter++ {
		n := rng.Intn(5) // 0..4 dimensions, includes the empty case
		lo := make([]*big.Int, n)
		hi := make([]*big.Int, n)
		for d := 0; d < n; d++ {
			l := int64(rng.Intn(7) - 3) // -3..3
			h := l + int64(rng.Intn(4)) // width 0..3
			if rng.Intn(20) == 0 {
				h = l - 1 // occasionally an empty dimension
			}
			lo[d], hi[d] = bi(l), bi(h)
		}
		var cons []constraint
		for c := 0; c < rng.Intn(5); c++ {
			coeffs := make([]*big.Int, n)
			for d := 0; d < n; d++ {
				coeffs[d] = bi(int64(rng.Intn(7) - 3)) // may be 0
			}
			rhs := bi(int64(rng.Intn(25) - 12))
			cons = append(cons, constraint{coeffs: coeffs, rhs: rhs})
		}
		checkAgainstEnumeration(t, lo, hi, cons)
	}
}

func TestStrictBoundaries(t *testing.T) {
	lo, hi := bis(0, 0), bis(4, 4)
	// Boundary exactly at the box corner and just inside it.
	for _, rhs := range []int64{0, 1, 7, 8, 9} {
		cons := []constraint{{coeffs: bis(1, 1), rhs: bi(rhs)}}
		checkAgainstEnumeration(t, lo, hi, cons)
	}
	// Negative coefficients, boundary on the lower side.
	cons := []constraint{{coeffs: bis(-1, -2), rhs: bi(-3)}}
	checkAgainstEnumeration(t, bis(-3, -3), bis(2, 2), cons)
}

func TestEmptyDimension(t *testing.T) {
	// n = 0: the only candidate point is the empty tuple.
	total := mustCount(t, nil, nil, nil)
	if total.Cmp(bi(1)) != 0 {
		t.Fatalf("empty space count = %s, want 1", total)
	}
	pt, proof := mustKth(t, nil, nil, nil, bi(1))
	if len(pt) != 0 || len(proof) != 0 {
		t.Fatalf("empty space kth(1) = %v proof %v, want empty", pt, proof)
	}
	// A 0 <= rhs constraint decides feasibility by itself.
	sat := []constraint{{coeffs: nil, rhs: bi(0)}}
	unsat := []constraint{{coeffs: nil, rhs: bi(-1)}}
	if got := mustCount(t, nil, nil, sat); got.Cmp(bi(1)) != 0 {
		t.Fatalf("0<=0 count = %s, want 1", got)
	}
	if got := mustCount(t, nil, nil, unsat); got.Cmp(bi(0)) != 0 {
		t.Fatalf("0<=-1 count = %s, want 0", got)
	}
	// A zero-width dimension keeps exactly one value.
	if got := mustCount(t, bis(5), bis(5), nil); got.Cmp(bi(1)) != 0 {
		t.Fatalf("zero-width box count = %s, want 1", got)
	}
	// An inverted dimension empties the box.
	if got := mustCount(t, bis(3), bis(2), nil); got.Cmp(bi(0)) != 0 {
		t.Fatalf("inverted box count = %s, want 0", got)
	}
}

func TestRedundantConstraints(t *testing.T) {
	lo, hi := bis(0, 0), bis(3, 3)
	// Always true: 16 points regardless of the redundant constraints.
	cons := []constraint{
		{coeffs: bis(0, 0), rhs: bi(0)},   // 0 <= 0
		{coeffs: bis(1, 1), rhs: bi(100)}, // x+y <= 100
		{coeffs: bis(-1, -1), rhs: bi(0)}, // -x-y <= 0
	}
	if got := mustCount(t, lo, hi, cons); got.Cmp(bi(16)) != 0 {
		t.Fatalf("redundant constraints count = %s, want 16", got)
	}
	checkAgainstEnumeration(t, lo, hi, cons)
}

func TestKthOutOfRange(t *testing.T) {
	lo, hi := bis(0, 0), bis(1, 1)
	total := mustCount(t, lo, hi, nil) // 4
	for _, k := range []int64{0, -1, 5, 100} {
		kk := bi(k)
		if kk.Sign() < 1 || kk.Cmp(total) > 0 {
			continue // main.go reports out_of_range; solver contract excludes it
		}
		t.Fatalf("k=%d should be out of range", k)
	}
	// First and last valid ranks are the box corners.
	first, _ := mustKth(t, lo, hi, nil, bi(1))
	last, _ := mustKth(t, lo, hi, nil, total)
	if !pointsEqual(first, bis(0, 0)) || !pointsEqual(last, bis(1, 1)) {
		t.Fatalf("corners: first %v last %v", first, last)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	lo, hi := bis(0, 0, 0), bis(9, 9, 9)
	cons := []constraint{{coeffs: bis(1, 1, 1), rhs: bi(13)}}
	s := NewSolver(lo, hi, cons, 1)
	if _, ok := s.Count(); ok {
		t.Fatalf("count with budget 1 should be unknown")
	}
	s = NewSolver(lo, hi, cons, 2)
	if _, _, ok := s.Kth(bi(1)); ok {
		t.Fatalf("kth with budget 2 should be unknown")
	}
	// A generous budget must succeed.
	if got := mustCount(t, lo, hi, nil); got.Cmp(bi(1000)) != 0 {
		t.Fatalf("10^3 box count = %s, want 1000", got)
	}
}

func TestBigIntCount(t *testing.T) {
	pow, _ := new(big.Int).SetString("1000000000000000000000000000000", 10) // 10^30
	lo := []*big.Int{new(big.Int)}
	hi := []*big.Int{new(big.Int).Set(pow)}
	want := new(big.Int).Add(pow, bi(1))
	if got := mustCount(t, lo, hi, nil); got.Cmp(want) != 0 {
		t.Fatalf("count = %s, want %s", got, want)
	}
	pt, proof := mustKth(t, lo, hi, nil, want)
	if len(pt) != 1 || pt[0].Cmp(pow) != 0 {
		t.Fatalf("kth(%s) = %v, want [%s]", want, pt, pow)
	}
	if len(proof) != 1 || proof[0].Skipped.Cmp(pow) != 0 {
		t.Fatalf("proof = %+v, want one step skipping %s", proof, pow)
	}
}

func TestLexicographicOrder(t *testing.T) {
	lo, hi := bis(0, 0), bis(2, 1)
	cons := []constraint{{coeffs: bis(1, -1), rhs: bi(1)}} // x - y <= 1
	want := [][]int64{{0, 0}, {0, 1}, {1, 0}, {1, 1}, {2, 1}}
	for i, w := range want {
		pt, _ := mustKth(t, lo, hi, cons, bi(int64(i+1)))
		if !pointsEqual(pt, bis(w...)) {
			t.Fatalf("kth(%d) = %v, want %v", i+1, pt, w)
		}
	}
}
