package treepath

import (
	"math/rand"
	"testing"
)

// Larger-scale cross-check: Mo sweep vs naive enumeration on a few
// thousand ops, with new colors appearing online throughout.
func TestStressAgainstNaive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in -short mode")
	}
	for seed := int64(100); seed < 105; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 500 + r.Intn(1500)
		edges, colors, ops := randomCase(r, n, 3000, 30)
		got := DistinctColorsOnPaths(n, edges, colors, ops, r.Intn(n))
		want := NaiveDistinctColorsOnPaths(n, edges, colors, ops)
		if !answersEqual(got, want) {
			t.Fatalf("seed %d: mismatch (%d queries)", seed, len(want))
		}
	}
}
