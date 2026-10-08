package quant

import (
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// bruteGroupCost searches every integer representative in [xs[a], xs[b]]
// and returns the minimum SSE, breaking ties toward the smaller value.
func bruteGroupCost(xs []int, w []int64, a, b int) (*big.Int, int) {
	var best *big.Int
	bestRep := 0
	for c := xs[a]; c <= xs[b]; c++ {
		sse := new(big.Int)
		for i := a; i <= b; i++ {
			d := big.NewInt(int64(xs[i] - c))
			d.Mul(d, d)
			d.Mul(d, big.NewInt(w[i]))
			sse.Add(sse, d)
		}
		if best == nil || sse.Cmp(best) < 0 {
			best = sse
			bestRep = c
		}
	}
	return best, bestRep
}

// bruteQuantize enumerates every contiguous partition of the sorted
// distinct levels into k groups and returns the optimal cost together
// with the lexicographically smallest optimal tuple of group starts.
func bruteQuantize(xs []int, w []int64, k int) (*big.Int, []int) {
	m := len(xs)
	if k > m {
		k = m
	}
	starts := make([]int, k+1)
	starts[k] = m
	var best *big.Int
	var bestStarts []int
	var rec func(j, lo int)
	rec = func(j, lo int) {
		if j == k {
			total := new(big.Int)
			for g := 0; g < k; g++ {
				c, _ := bruteGroupCost(xs, w, starts[g], starts[g+1]-1)
				total.Add(total, c)
			}
			if best == nil || total.Cmp(best) < 0 ||
				(total.Cmp(best) == 0 && lessInts(starts[:k], bestStarts)) {
				best = total
				bestStarts = append([]int(nil), starts[:k]...)
			}
			return
		}
		for s := lo; s <= m-(k-j); s++ {
			starts[j] = s
			rec(j+1, s+1)
		}
	}
	rec(1, 1) // the first group always starts at index 0
	return best, bestStarts
}

func distinct(levels []int) ([]int, []int64) {
	agg := map[int]int64{}
	for _, g := range levels {
		agg[g]++
	}
	xs := make([]int, 0, len(agg))
	for g := range agg {
		xs = append(xs, g)
	}
	sort.Ints(xs)
	w := make([]int64, len(xs))
	for i, g := range xs {
		w[i] = agg[g]
	}
	return xs, w
}

func checkAgainstBrute(t *testing.T, levels []int, weights []int64, k int) {
	t.Helper()
	res := Quantize(levels, weights, k)
	xs := make([]int, 0, len(levels))
	seen := map[int]bool{}
	for _, g := range levels {
		if !seen[g] {
			seen[g] = true
			xs = append(xs, g)
		}
	}
	sort.Ints(xs)
	agg := map[int]int64{}
	for i, g := range levels {
		agg[g] += weights[i]
	}
	w := make([]int64, len(xs))
	for i, g := range xs {
		w[i] = agg[g]
	}

	wantCost, wantStarts := bruteQuantize(xs, w, k)
	if res.SSE.Cmp(wantCost) != 0 {
		t.Fatalf("Quantize(%v, %v, %d): SSE=%v, brute force=%v",
			levels, weights, k, res.SSE, wantCost)
	}
	kk := k
	if kk > len(xs) {
		kk = len(xs)
	}
	if len(res.Groups) != kk {
		t.Fatalf("Quantize(%v, %v, %d): got %d groups, want %d",
			levels, weights, k, len(res.Groups), kk)
	}
	// Verify group boundaries and representatives.
	for gi, grp := range res.Groups {
		a := wantStarts[gi]
		b := 0
		if gi+1 < kk {
			b = wantStarts[gi+1] - 1
		} else {
			b = len(xs) - 1
		}
		if grp.Min != xs[a] || grp.Max != xs[b] {
			t.Fatalf("Quantize(%v, %v, %d): group %d = [%d,%d], brute force=[%d,%d]",
				levels, weights, k, gi, grp.Min, grp.Max, xs[a], xs[b])
		}
		gc, rep := bruteGroupCost(xs, w, a, b)
		if grp.Rep != rep {
			t.Fatalf("Quantize(%v, %v, %d): group %d rep=%d, integer search=%d",
				levels, weights, k, gi, grp.Rep, rep)
		}
		if grp.SSE.Cmp(gc) != 0 {
			t.Fatalf("Quantize(%v, %v, %d): group %d SSE=%v, integer search=%v",
				levels, weights, k, gi, grp.SSE, gc)
		}
		for _, x := range grp.Levels {
			if res.Rep[x] != grp.Rep {
				t.Fatalf("Quantize(%v, %v, %d): Rep[%d]=%d, group rep=%d",
					levels, weights, k, x, res.Rep[x], grp.Rep)
			}
		}
	}
	// Every distinct level must be mapped.
	for _, x := range xs {
		if _, ok := res.Rep[x]; !ok {
			t.Fatalf("Quantize(%v, %v, %d): level %d unmapped", levels, weights, k, x)
		}
	}
}

func TestQuantizeDuplicatesAndZeroWeights(t *testing.T) {
	// Duplicate colors and zero-weight levels.
	levels := []int{5, 5, 1, 9, 1, 5, 9}
	weights := []int64{2, 3, 0, 4, 1, 1, 0}
	for k := 1; k <= 4; k++ {
		checkAgainstBrute(t, levels, weights, k)
	}
}

func TestQuantizeAllZeroWeights(t *testing.T) {
	levels := []int{3, 1, 2}
	weights := []int64{0, 0, 0}
	res := Quantize(levels, weights, 2)
	if res.SSE.Sign() != 0 {
		t.Fatalf("SSE=%v, want 0", res.SSE)
	}
	// Canonical tie-break: lexicographically smallest starts, so the
	// first group is {1} and each zero-weight group takes its smallest
	// level as representative.
	if len(res.Groups) != 2 || res.Groups[0].Min != 1 || res.Groups[0].Max != 1 {
		t.Fatalf("unexpected canonical groups: %+v", res.Groups)
	}
	if res.Groups[0].Rep != 1 || res.Groups[1].Rep != 2 {
		t.Fatalf("zero-weight reps should be the smallest level: %+v", res.Groups)
	}
}

func TestQuantizeTieGroups(t *testing.T) {
	// Two equally optimal partitions: {[0],[10,20]} and {[0,10],[20]}
	// both have cost 0. The lexicographically smaller starts win.
	levels := []int{0, 10, 20}
	weights := []int64{1, 0, 1}
	res := Quantize(levels, weights, 2)
	if res.SSE.Sign() != 0 {
		t.Fatalf("SSE=%v, want 0", res.SSE)
	}
	if res.Groups[0].Max != 0 {
		t.Fatalf("canonical partition should start with {[0],...}: %+v", res.Groups)
	}
	// And the representative tie: group {0,10} with equal weights has
	// centroid 5; group {0, 9} has real centroid 4.5 -> integer tie
	// between 4 and 5, resolved to 4.
	res2 := Quantize([]int{0, 9}, []int64{1, 1}, 1)
	if res2.Groups[0].Rep != 4 {
		t.Fatalf("centroid tie should resolve to the smaller integer, got %d",
			res2.Groups[0].Rep)
	}
}

func TestQuantizeKExceedsLevels(t *testing.T) {
	res := Quantize([]int{7, 3}, []int64{2, 5}, 10)
	if len(res.Groups) != 2 || res.SSE.Sign() != 0 {
		t.Fatalf("k > distinct levels should clamp: %+v", res)
	}
}

func TestQuantizeRandomVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(12) // up to twelve distinct levels
		levels := make([]int, 0, n)
		for i := 0; i < n; i++ {
			g := rng.Intn(40) - 5 // may duplicate; may be negative
			reps := 1 + rng.Intn(2)
			for r := 0; r < reps; r++ {
				levels = append(levels, g)
			}
		}
		weights := make([]int64, len(levels))
		for i := range weights {
			weights[i] = int64(rng.Intn(6)) // includes zeros
		}
		k := 1 + rng.Intn(14)
		checkAgainstBrute(t, levels, weights, k)
	}
}

func TestQuantizeMappingContiguous(t *testing.T) {
	// The mapping must be monotone: sorted levels map to sorted reps.
	res := Quantize([]int{0, 1, 2, 10, 11, 12, 20, 21}, []int64{3, 1, 1, 1, 5, 1, 2, 2}, 3)
	last := -1 << 30
	keys := make([]int, 0, len(res.Rep))
	for x := range res.Rep {
		keys = append(keys, x)
	}
	sort.Ints(keys)
	for _, x := range keys {
		if res.Rep[x] < last {
			t.Fatalf("non-contiguous mapping: %v", res.Rep)
		}
		last = res.Rep[x]
	}
	if !reflect.DeepEqual(keys, []int{0, 1, 2, 10, 11, 12, 20, 21}) {
		t.Fatalf("mapping keys: %v", keys)
	}
}
