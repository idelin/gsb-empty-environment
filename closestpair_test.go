package closestpair

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// bruteForce is the independent O(n^2) reference implementation.
func bruteForce(points []Point) (*big.Int, []Pair) {
	var best *big.Int
	var pairs []Pair
	for i := 0; i < len(points); i++ {
		for j := i + 1; j < len(points); j++ {
			d := dist2(points[i], points[j])
			switch {
			case best == nil || d.Cmp(best) < 0:
				best = d
				pairs = []Pair{makePair(points[i], points[j])}
			case d.Cmp(best) == 0:
				pairs = append(pairs, makePair(points[i], points[j]))
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].First != pairs[j].First {
			return pairs[i].First < pairs[j].First
		}
		return pairs[i].Second < pairs[j].Second
	})
	return best, pairs
}

func checkAgainstBruteForce(t *testing.T, points []Point) *Result {
	t.Helper()
	res, err := Query(points)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	wantDist, wantPairs := bruteForce(points)
	if res.MinDist2().Cmp(wantDist) != 0 {
		t.Fatalf("MinDist2 = %s, want %s", res.MinDist2(), wantDist)
	}
	got := res.Pairs(0, -1)
	if res.NumPairs() != len(wantPairs) || len(got) != len(wantPairs) {
		t.Fatalf("NumPairs = %d, want %d", res.NumPairs(), len(wantPairs))
	}
	for i := range wantPairs {
		if got[i] != wantPairs[i] {
			t.Fatalf("pair %d = %+v, want %+v", i, got[i], wantPairs[i])
		}
	}
	if !sort.IsSorted(pairSlice(got)) {
		t.Fatal("pairs are not sorted by ID")
	}
	return res
}

type pairSlice []Pair

func (p pairSlice) Len() int { return len(p) }
func (p pairSlice) Less(i, j int) bool {
	if p[i].First != p[j].First {
		return p[i].First < p[j].First
	}
	return p[i].Second < p[j].Second
}
func (p pairSlice) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		n := 2 + rng.Intn(40)
		// Small coordinate range to force frequent ties and duplicates.
		span := int64(1 + rng.Intn(6))
		pts := make([]Point, n)
		for i := range pts {
			pts[i] = Point{
				ID: int64(i),
				X:  int64(rng.Intn(int(2*span+1))) - span,
				Y:  int64(rng.Intn(int(2*span+1))) - span,
			}
		}
		// Shuffle IDs so input order and ID order differ.
		rng.Shuffle(n, func(i, j int) { pts[i], pts[j] = pts[j], pts[i] })
		checkAgainstBruteForce(t, pts)
	}
}

func TestMidlineCrossingPairs(t *testing.T) {
	// Two vertical columns at x=0 and x=1: every closest pair crosses
	// the dividing line x in (0, 1).
	var pts []Point
	for i := 0; i < 6; i++ {
		pts = append(pts,
			Point{ID: int64(2 * i), X: 0, Y: int64(10 * i)},
			Point{ID: int64(2*i + 1), X: 1, Y: int64(10 * i)},
		)
	}
	res := checkAgainstBruteForce(t, pts)
	if res.MinDist2().Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("MinDist2 = %s, want 1", res.MinDist2())
	}
	if res.NumPairs() != 6 {
		t.Fatalf("NumPairs = %d, want 6", res.NumPairs())
	}
	for _, p := range res.Pairs(0, -1) {
		if p.Second-p.First != 1 || p.First%2 != 0 {
			t.Fatalf("pair %+v is not a cross-midline pair", p)
		}
	}
}

func TestDenseGridHeavyTies(t *testing.T) {
	// Larger n crammed onto a tiny grid: many duplicate coordinates and
	// many tied pairs, exercising the quadratic-output path.
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		n := 150 + rng.Intn(100)
		pts := make([]Point, n)
		for i := range pts {
			pts[i] = Point{
				ID: int64(i),
				X:  int64(rng.Intn(3)) - 1,
				Y:  int64(rng.Intn(3)) - 1,
			}
		}
		checkAgainstBruteForce(t, pts)
	}
	// All points coincident: every pair ties at distance 0.
	n := 120
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{ID: int64(i), X: 4, Y: 4}
	}
	res := checkAgainstBruteForce(t, pts)
	if res.NumPairs() != n*(n-1)/2 {
		t.Fatalf("NumPairs = %d, want %d", res.NumPairs(), n*(n-1)/2)
	}
}

func TestCoCircularPoints(t *testing.T) {
	// Twelve lattice points on the circle x^2 + y^2 = 25.
	pts := []Point{
		{ID: 0, X: 5, Y: 0}, {ID: 1, X: -5, Y: 0},
		{ID: 2, X: 0, Y: 5}, {ID: 3, X: 0, Y: -5},
		{ID: 4, X: 3, Y: 4}, {ID: 5, X: 3, Y: -4},
		{ID: 6, X: -3, Y: 4}, {ID: 7, X: -3, Y: -4},
		{ID: 8, X: 4, Y: 3}, {ID: 9, X: 4, Y: -3},
		{ID: 10, X: -4, Y: 3}, {ID: 11, X: -4, Y: -3},
	}
	res := checkAgainstBruteForce(t, pts)
	// The closest pairs are (3,+-4)-(4,+-3) and (-3,+-4)-(-4,+-3):
	// chord squared distance 2, four pairs in total.
	if res.MinDist2().Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("MinDist2 = %s, want 2", res.MinDist2())
	}
	if res.NumPairs() != 4 {
		t.Fatalf("NumPairs = %d, want 4", res.NumPairs())
	}
}

func TestDuplicatePointGroups(t *testing.T) {
	var pts []Point
	id := int64(0)
	add := func(x, y int64, count int) {
		for i := 0; i < count; i++ {
			pts = append(pts, Point{ID: id, X: x, Y: y})
			id++
		}
	}
	add(2, 2, 5)  // C(5,2) = 10 pairs at distance 0
	add(9, 9, 3)  // C(3,2) = 3 pairs at distance 0
	add(-7, 4, 1) // isolated
	add(2, 5, 2)  // C(2,2) = 1 pair at distance 0
	res := checkAgainstBruteForce(t, pts)
	if res.MinDist2().Sign() != 0 {
		t.Fatalf("MinDist2 = %s, want 0", res.MinDist2())
	}
	if res.NumPairs() != 10+3+1 {
		t.Fatalf("NumPairs = %d, want 14", res.NumPairs())
	}
}

func TestLargeCoordinatesNoOverflow(t *testing.T) {
	pts := []Point{
		{ID: 0, X: math.MaxInt64, Y: math.MaxInt64},
		{ID: 1, X: math.MinInt64, Y: math.MinInt64},
		{ID: 2, X: math.MaxInt64, Y: math.MinInt64},
		{ID: 3, X: math.MinInt64, Y: math.MaxInt64},
	}
	res := checkAgainstBruteForce(t, pts)
	// Closest pairs are the sides of the square: dx = dy = 2^64 - 1,
	// so dist2 = (2^64 - 1)^2, which exceeds 127 bits and would
	// overflow int64/int128 arithmetic.
	side := new(big.Int).Sub(big.NewInt(math.MaxInt64), big.NewInt(math.MinInt64))
	want := new(big.Int).Mul(side, side)
	if res.MinDist2().Cmp(want) != 0 {
		t.Fatalf("MinDist2 = %s, want %s", res.MinDist2(), want)
	}
	if res.NumPairs() != 4 {
		t.Fatalf("NumPairs = %d, want 4", res.NumPairs())
	}
}

func TestPagination(t *testing.T) {
	// 40 coincident points -> C(40,2) = 780 tied pairs.
	var pts []Point
	for i := 0; i < 40; i++ {
		pts = append(pts, Point{ID: int64(i), X: 7, Y: -3})
	}
	pts = append(pts, Point{ID: 100, X: 0, Y: 0})
	res := checkAgainstBruteForce(t, pts)

	all := res.Pairs(0, -1)
	if len(all) != 780 {
		t.Fatalf("total pairs = %d, want 780", len(all))
	}
	for _, pageSize := range []int{1, 2, 3, 7, 64, 779, 780, 1000} {
		var got []Pair
		for offset := 0; ; offset += pageSize {
			page := res.Pairs(offset, pageSize)
			got = append(got, page...)
			if len(page) < pageSize {
				break
			}
		}
		if len(got) != len(all) {
			t.Fatalf("pageSize %d: collected %d pairs, want %d", pageSize, len(got), len(all))
		}
		for i := range all {
			if got[i] != all[i] {
				t.Fatalf("pageSize %d: pair %d = %+v, want %+v", pageSize, i, got[i], all[i])
			}
		}
	}
	if got := res.Pairs(780, 10); len(got) != 0 {
		t.Fatalf("offset beyond end returned %d pairs, want 0", len(got))
	}
	if got := res.Pairs(779, 10); len(got) != 1 {
		t.Fatalf("last partial page returned %d pairs, want 1", len(got))
	}
	if got := res.Pairs(-5, 3); len(got) != 3 || got[0] != all[0] {
		t.Fatalf("negative offset not clamped: %+v", got)
	}
	// Mutating a returned page must not affect subsequent pages.
	page := res.Pairs(0, 5)
	page[0] = Pair{First: -1, Second: -1}
	if res.Pairs(0, 5)[0] != all[0] {
		t.Fatal("returned page aliases internal storage")
	}
}

func TestErrors(t *testing.T) {
	if _, err := Query(nil); err == nil {
		t.Fatal("expected error for empty input")
	}
	if _, err := Query([]Point{{ID: 1, X: 0, Y: 0}}); err == nil {
		t.Fatal("expected error for single point")
	}
	dup := []Point{{ID: 1, X: 0, Y: 0}, {ID: 1, X: 5, Y: 5}}
	if _, err := Query(dup); err == nil {
		t.Fatal("expected error for duplicate IDs")
	}
}

func TestResultSortedByID(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 100; trial++ {
		n := 2 + rng.Intn(30)
		pts := make([]Point, n)
		for i := range pts {
			pts[i] = Point{ID: int64(i), X: int64(rng.Intn(4)), Y: int64(rng.Intn(4))}
		}
		res := checkAgainstBruteForce(t, pts)
		for _, p := range res.Pairs(0, -1) {
			if p.First >= p.Second {
				t.Fatalf("pair %+v not normalized (First < Second)", p)
			}
		}
	}
}

func ExampleQuery() {
	res, _ := Query([]Point{
		{ID: 10, X: 0, Y: 0},
		{ID: 20, X: 3, Y: 4},
		{ID: 30, X: 0, Y: 1},
	})
	fmt.Println(res.MinDist2())
	for _, p := range res.Pairs(0, -1) {
		fmt.Println(p.First, p.Second)
	}
	// Output:
	// 1
	// 10 30
}
