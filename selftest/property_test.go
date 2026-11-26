package selftest_test

import (
	"math/rand"
	"testing"

	temporal "prescreen.local/ps-db7888bb15"
)

type fuzzSet map[int64]bool

func fuzzRangeSet(r temporal.Range, max int64) fuzzSet {
	r = r.Normalize()
	m := fuzzSet{}
	for x := int64(0); x < max; x++ {
		if r.Contains(x) {
			m[x] = true
		}
	}
	return m
}

func (m fuzzSet) equals(o fuzzSet) bool {
	if len(m) != len(o) {
		return false
	}
	for k := range m {
		if !o[k] {
			return false
		}
	}
	return true
}

// Randomized cross-check of intersect/difference/union/coverage against an
// explicit integer-set model over a small domain window.
func TestAccPropertyAlgebra(t *testing.T) {
	const n, window = 12, int64(14)
	rng := rand.New(rand.NewSource(7))
	randRange := func() temporal.Range {
		a := rng.Int63n(n + 1)
		b := rng.Int63n(n + 1)
		return temporal.NewDiscreteRange(a, b, rng.Intn(2) == 0, rng.Intn(2) == 0)
	}

	for i := 0; i < 5000; i++ {
		a, b := randRange(), randRange()
		sa, sb := fuzzRangeSet(a, window), fuzzRangeSet(b, window)

		wantI := fuzzSet{}
		for x := range sa {
			if sb[x] {
				wantI[x] = true
			}
		}
		if got := fuzzRangeSet(temporal.Intersect(a, b), window); !got.equals(wantI) {
			t.Fatalf("intersect %s & %s = %v want %v", a, b, got, wantI)
		}
		if temporal.Overlaps(a, b) != (len(wantI) > 0) {
			t.Fatalf("overlaps mismatch %s %s", a, b)
		}

		wantD := fuzzSet{}
		for x := range sa {
			if !sb[x] {
				wantD[x] = true
			}
		}
		gotD := fuzzSet{}
		for _, p := range temporal.Difference(a, b) {
			for x := range fuzzRangeSet(p, window) {
				if gotD[x] {
					t.Fatalf("difference pieces overlap: %s - %s -> %v", a, b, temporal.Difference(a, b))
				}
				gotD[x] = true
			}
		}
		if !gotD.equals(wantD) {
			t.Fatalf("difference %s - %s = %v want %v", a, b, gotD, wantD)
		}

		wantU := fuzzSet{}
		for x := range sa {
			wantU[x] = true
		}
		for x := range sb {
			wantU[x] = true
		}
		gotU := fuzzSet{}
		for _, p := range temporal.Union(a, b) {
			for x := range fuzzRangeSet(p, window) {
				gotU[x] = true
			}
		}
		if !gotU.equals(wantU) {
			t.Fatalf("union %s | %s = %v want %v", a, b, gotU, wantU)
		}

		subset := true
		for x := range sa {
			if !sb[x] {
				subset = false
			}
		}
		if temporal.CoveredBy(a, []temporal.Range{b}) != subset {
			t.Fatalf("coveredby %s in %s mismatch", a, b)
		}
	}
}
