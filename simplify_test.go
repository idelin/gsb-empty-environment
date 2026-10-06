package polysimp

import (
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

// lessSeq reports whether index sequence a is lexicographically smaller
// than b.
func lessSeq(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// bruteForce enumerates every subset of interior indices, keeps those that
// form a valid simplification, and returns the lexicographically smallest
// minimum-cardinality one. It is the independent oracle for Simplify.
func bruteForce(pts []Point, mandatory []int, threshold *big.Rat) []int {
	n := len(pts)
	if n == 0 {
		return nil
	}
	keep := make([]bool, n)
	keep[0] = true
	keep[n-1] = true
	for _, m := range mandatory {
		keep[m] = true
	}
	interior := n - 2
	var best []int
	for mask := 0; mask < (1 << uint(interior)); mask++ {
		sel := make([]bool, n)
		copy(sel, keep)
		for b := 0; b < interior; b++ {
			if mask&(1<<uint(b)) != 0 {
				sel[b+1] = true
			}
		}
		var seq []int
		for i := 0; i < n; i++ {
			if sel[i] {
				seq = append(seq, i)
			}
		}
		ok := true
		for e := 0; e+1 < len(seq); e++ {
			if !EdgeValid(pts, seq[e], seq[e+1], threshold) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if best == nil || len(seq) < len(best) ||
			(len(seq) == len(best) && lessSeq(seq, best)) {
			best = append([]int(nil), seq...)
		}
	}
	return best
}

func TestSegDistSq(t *testing.T) {
	cases := []struct {
		p, a, b Point
		want    *big.Rat
	}{
		// Perpendicular foot inside the segment.
		{Point{0, 1}, Point{0, 0}, Point{2, 0}, big.NewRat(1, 1)},
		// Projection past b: clamped to endpoint b.
		{Point{3, 4}, Point{0, 0}, Point{1, 0}, big.NewRat(20, 1)},
		// Projection before a: clamped to endpoint a.
		{Point{-1, 0}, Point{0, 0}, Point{1, 0}, big.NewRat(1, 1)},
		// Degenerate segment (duplicate endpoints).
		{Point{4, 5}, Point{1, 1}, Point{1, 1}, big.NewRat(25, 1)},
		// Point exactly on the segment.
		{Point{1, 1}, Point{0, 0}, Point{2, 2}, big.NewRat(0, 1)},
		// Non-axis-aligned interior projection: p=(1,1) onto (0,0)-(3,1).
		// t = 4/10 = 2/5, foot = (6/5, 2/5), d^2 = 1/25 + 9/25 = 2/5.
		{Point{1, 1}, Point{0, 0}, Point{3, 1}, big.NewRat(2, 5)},
	}
	for i, c := range cases {
		got := SegDistSq(c.p, c.a, c.b)
		if got.Cmp(c.want) != 0 {
			t.Errorf("case %d: SegDistSq(%v, %v, %v) = %v, want %v",
				i, c.p, c.a, c.b, got, c.want)
		}
	}
}

func TestSimplifyDeterministic(t *testing.T) {
	thr := func(num, den int64) *big.Rat { return big.NewRat(num, den) }
	cases := []struct {
		name      string
		pts       []Point
		mandatory []int
		threshold *big.Rat
		want      []int
	}{
		{
			name:      "empty",
			pts:       nil,
			threshold: thr(1, 1),
			want:      nil,
		},
		{
			name:      "single point",
			pts:       []Point{{5, 5}},
			threshold: thr(0, 1),
			want:      []int{0},
		},
		{
			name:      "collinear zero tolerance keeps endpoints only",
			pts:       []Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}},
			threshold: thr(0, 1),
			want:      []int{0, 3},
		},
		{
			name:      "mandatory midpoint forces retention",
			pts:       []Point{{0, 0}, {1, 0}, {2, 0}},
			mandatory: []int{1},
			threshold: thr(0, 1),
			want:      []int{0, 1, 2},
		},
		{
			name:      "duplicate coordinates tolerated at zero tolerance",
			pts:       []Point{{0, 0}, {0, 0}, {1, 0}, {1, 0}},
			threshold: thr(0, 1),
			want:      []int{0, 3},
		},
		{
			name:      "duplicate endpoints with excursion",
			pts:       []Point{{1, 1}, {1, 1}, {2, 5}, {1, 1}},
			threshold: thr(0, 1),
			want:      []int{0, 2, 3},
		},
		{
			// Both {0,1,3} and {0,2,3} are minimal; lexicographic wins.
			name:      "lexicographic tie broken by smaller indices",
			pts:       []Point{{0, 0}, {1, 1}, {2, 1}, {3, 0}},
			threshold: thr(1, 2),
			want:      []int{0, 1, 3},
		},
		{
			// Greedy farthest-point (RDP) splitting keeps 4 vertices
			// ([0 1 4 5]); the true optimum keeps only 3.
			name:      "beats greedy farthest point",
			pts:       []Point{{1, -1}, {1, 1}, {1, 0}, {2, 0}, {1, -1}, {3, 0}},
			threshold: thr(3, 1),
			want:      []int{0, 2, 5},
		},
		{
			name:      "negative tolerance keeps every vertex",
			pts:       []Point{{0, 0}, {1, 0}, {2, 0}},
			threshold: thr(-1, 1),
			want:      []int{0, 1, 2},
		},
		{
			name:      "mandatory endpoints are no-ops",
			pts:       []Point{{0, 0}, {1, 1}, {2, 0}},
			mandatory: []int{0, 2},
			threshold: thr(10, 1),
			want:      []int{0, 2},
		},
	}
	for _, c := range cases {
		got := Simplify(c.pts, c.mandatory, c.threshold)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Simplify = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSimplifyAgainstBruteForce cross-checks Simplify against exhaustive
// subset enumeration on random polylines, including duplicate coordinates,
// mandatory sets, and zero/negative/fractional thresholds.
func TestSimplifyAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	thresholds := []*big.Rat{
		big.NewRat(-1, 1),
		big.NewRat(0, 1),
		big.NewRat(1, 3),
		big.NewRat(1, 1),
		big.NewRat(2, 1),
		big.NewRat(7, 2),
		big.NewRat(50, 1),
	}
	for trial := 0; trial < 3000; trial++ {
		n := 2 + rng.Intn(10) // 2..11 vertices
		pts := make([]Point, n)
		for i := range pts {
			// Small coordinate range makes duplicates frequent.
			pts[i] = Point{int64(rng.Intn(7) - 3), int64(rng.Intn(7) - 3)}
		}
		var mandatory []int
		for i := 0; i < n; i++ {
			if rng.Intn(4) == 0 {
				mandatory = append(mandatory, i)
			}
		}
		threshold := thresholds[rng.Intn(len(thresholds))]

		got := Simplify(pts, mandatory, threshold)
		want := bruteForce(pts, mandatory, threshold)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: pts=%v mandatory=%v threshold=%v\nSimplify=%v\nbruteForce=%v",
				trial, pts, mandatory, threshold, got, want)
		}

		// Sanity: result retains endpoints and mandatory indices, and
		// every edge is exactly valid.
		if got[0] != 0 || got[len(got)-1] != n-1 {
			t.Fatalf("trial %d: endpoints not retained: %v", trial, got)
		}
		inSeq := make(map[int]bool, len(got))
		for _, idx := range got {
			inSeq[idx] = true
		}
		for _, m := range mandatory {
			if !inSeq[m] {
				t.Fatalf("trial %d: mandatory index %d missing from %v", trial, m, got)
			}
		}
		for e := 0; e+1 < len(got); e++ {
			if !EdgeValid(pts, got[e], got[e+1], threshold) {
				t.Fatalf("trial %d: invalid edge %d->%d in %v", trial, got[e], got[e+1], got)
			}
		}
	}
}
