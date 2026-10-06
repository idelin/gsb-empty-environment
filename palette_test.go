package quant

import (
	"math/rand"
	"reflect"
	"testing"
)

// bruteForcePalette enumerates every composition of the sorted distinct
// values into k contiguous groups and, for each group, tries every
// integer representative in [Lo, Hi]. It is an independent reference
// implementation used to cross-check Quantize.
func bruteForcePalette(levels []Level, k int) int64 {
	vals, ws, err := normalize(levels)
	if err != nil {
		panic(err)
	}
	n := len(vals)
	if k > n {
		k = n
	}
	groupBest := func(lo, hi int) int64 {
		best := int64(1<<62 - 1)
		for r := vals[lo]; r <= vals[hi-1]; r++ {
			var c int64
			for i := lo; i < hi; i++ {
				d := int64(vals[i] - r)
				c += ws[i] * d * d
			}
			if c < best {
				best = c
			}
		}
		return best
	}
	best := int64(1<<62 - 1)
	var rec func(start, left int, acc int64)
	rec = func(start, left int, acc int64) {
		if left == 0 {
			if acc+groupBest(start, n) < best {
				best = acc + groupBest(start, n)
			}
			return
		}
		for c := start + 1; c <= n-left; c++ {
			rec(c, left-1, acc+groupBest(start, c))
		}
	}
	rec(0, k-1, 0)
	return best
}

func TestQuantizeBasic(t *testing.T) {
	res, err := Quantize([]Level{{0, 5}, {10, 5}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cost != 0 {
		t.Fatalf("cost = %d, want 0", res.Cost)
	}
	if !reflect.DeepEqual(res.Palette, []int{0, 10}) {
		t.Fatalf("palette = %v", res.Palette)
	}
	if res.Rep[0] != 0 || res.Rep[10] != 10 {
		t.Fatalf("mapping = %v", res.Rep)
	}
}

func TestQuantizeCentroidTieTakesSmaller(t *testing.T) {
	// Weighted mean is exactly 4.5; both 4 and 5 cost the same, the
	// smaller representative must win.
	res, err := Quantize([]Level{{4, 1}, {5, 1}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 1 || res.Groups[0].Rep != 4 {
		t.Fatalf("groups = %+v", res.Groups)
	}
	if res.Cost != 1 { // (4-4)^2 + (5-4)^2
		t.Fatalf("cost = %d, want 1", res.Cost)
	}
}

func TestQuantizeDuplicateColors(t *testing.T) {
	res, err := Quantize([]Level{{0, 1}, {0, 2}, {8, 1}, {0, 1}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("groups = %+v", res.Groups)
	}
	if res.Groups[0].Weight != 4 || res.Groups[0].Rep != 0 {
		t.Fatalf("group0 = %+v", res.Groups[0])
	}
	if res.Groups[1].Weight != 1 || res.Groups[1].Rep != 8 {
		t.Fatalf("group1 = %+v", res.Groups[1])
	}
	if res.Cost != 0 {
		t.Fatalf("cost = %d, want 0", res.Cost)
	}
}

func TestQuantizeZeroWeights(t *testing.T) {
	// The zero-weight level 3 costs nothing wherever it lands, but it
	// must still appear in exactly one group and in the mapping.
	res, err := Quantize([]Level{{0, 4}, {3, 0}, {9, 4}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("groups = %+v", res.Groups)
	}
	seen := 0
	for _, g := range res.Groups {
		if g.Lo <= 3 && 3 <= g.Hi {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("level 3 covered by %d groups: %+v", seen, res.Groups)
	}
	if _, ok := res.Rep[3]; !ok {
		t.Fatalf("mapping missing zero-weight level: %v", res.Rep)
	}
	if res.Cost != bruteForcePalette([]Level{{0, 4}, {3, 0}, {9, 4}}, 2) {
		t.Fatalf("cost = %d, brute force = %d", res.Cost, bruteForcePalette([]Level{{0, 4}, {3, 0}}, 2))
	}
}

func TestQuantizeMultipleOptimalPartitions(t *testing.T) {
	// Both [0|1,2] and [0,1|2] cost 1 (the size-2 group gets the
	// smaller centroid on the .5 tie). The lexicographically smallest
	// cut tuple, i.e. cutting after the first value, must be returned.
	res, err := Quantize([]Level{{0, 1}, {1, 1}, {2, 1}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cost != 1 {
		t.Fatalf("cost = %d, want 1", res.Cost)
	}
	want := []Group{
		{Lo: 0, Hi: 0, Rep: 0, Weight: 1, Cost: 0},
		{Lo: 1, Hi: 2, Rep: 1, Weight: 2, Cost: 1},
	}
	if !reflect.DeepEqual(res.Groups, want) {
		t.Fatalf("groups = %+v, want %+v", res.Groups, want)
	}
}

func TestQuantizeClampsK(t *testing.T) {
	res, err := Quantize([]Level{{2, 1}, {7, 1}}, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 || res.Cost != 0 {
		t.Fatalf("groups = %+v cost = %d", res.Groups, res.Cost)
	}
}

func TestQuantizeErrors(t *testing.T) {
	if _, err := Quantize(nil, 2); err == nil {
		t.Fatal("expected error for empty levels")
	}
	if _, err := Quantize([]Level{{1, 1}}, 0); err == nil {
		t.Fatal("expected error for k = 0")
	}
	if _, err := Quantize([]Level{{1, -1}}, 1); err == nil {
		t.Fatal("expected error for negative weight")
	}
}

func TestQuantizeMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(8) // up to 8 distinct values
		perm := rng.Perm(16)[:n]
		levels := make([]Level, 0, n+2)
		for _, v := range perm {
			levels = append(levels, Level{Value: v, Weight: int64(rng.Intn(6))}) // includes zero weights
		}
		// Sprinkle duplicate colors.
		levels = append(levels, Level{Value: perm[0], Weight: int64(rng.Intn(3))})
		k := 1 + rng.Intn(n)
		res, err := Quantize(levels, k)
		if err != nil {
			t.Fatal(err)
		}
		want := bruteForcePalette(levels, k)
		if res.Cost != want {
			t.Fatalf("trial %d: levels=%v k=%d cost=%d brute=%d", trial, levels, k, res.Cost, want)
		}
		// Structural invariants: groups contiguous, sorted, covering
		// every level exactly once; mapping consistent with groups.
		if len(res.Groups) != k {
			t.Fatalf("trial %d: %d groups, want %d", trial, len(res.Groups), k)
		}
		covered := map[int]int{}
		for gi, g := range res.Groups {
			if g.Rep < g.Lo || g.Rep > g.Hi {
				t.Fatalf("trial %d: rep %d outside [%d,%d]", trial, g.Rep, g.Lo, g.Hi)
			}
			if gi > 0 && res.Groups[gi-1].Hi >= g.Lo {
				t.Fatalf("trial %d: groups overlap or unsorted: %+v", trial, res.Groups)
			}
			for v := g.Lo; v <= g.Hi; v++ {
				if _, ok := res.Rep[v]; ok {
					covered[v]++
					if res.Rep[v] != g.Rep {
						t.Fatalf("trial %d: Rep[%d]=%d, group rep %d", trial, v, res.Rep[v], g.Rep)
					}
				}
			}
		}
		vals, _, _ := normalize(levels)
		if len(covered) != len(vals) {
			t.Fatalf("trial %d: mapping covers %d of %d levels", trial, len(covered), len(vals))
		}
		for _, v := range vals {
			if covered[v] != 1 {
				t.Fatalf("trial %d: level %d covered %d times", trial, v, covered[v])
			}
		}
	}
}
