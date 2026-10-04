package closestpair

import (
	"math"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// bruteForce is the all-pairs reference implementation.
func bruteForce(points []Point) (*big.Int, []Pair) {
	var best *big.Int
	var pairs []Pair
	for i := 0; i < len(points); i++ {
		for j := i + 1; j < len(points); j++ {
			dx := big.NewInt(points[i].X - points[j].X) // test coords kept small
			dy := big.NewInt(points[i].Y - points[j].Y)
			dx.Mul(dx, dx)
			dy.Mul(dy, dy)
			d := dx.Add(dx, dy)
			switch {
			case best == nil || d.Cmp(best) < 0:
				best = d
				pairs = []Pair{newPair(points[i].ID, points[j].ID)}
			case d.Cmp(best) == 0:
				pairs = append(pairs, newPair(points[i].ID, points[j].ID))
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	})
	return best, pairs
}

func fetchAll(t *testing.T, points []Point) (*big.Int, []Pair) {
	t.Helper()
	res, err := ClosestPairs(points, 0, math.MaxInt32)
	if err != nil {
		t.Fatalf("ClosestPairs: %v", err)
	}
	if res.Total != len(res.Pairs) {
		t.Fatalf("Total=%d but got %d pairs", res.Total, len(res.Pairs))
	}
	return res.Dist2, res.Pairs
}

func checkAgainstBruteForce(t *testing.T, points []Point) {
	t.Helper()
	wantD, wantPairs := bruteForce(points)
	gotD, gotPairs := fetchAll(t, points)
	if gotD.Cmp(wantD) != 0 {
		t.Fatalf("Dist2=%s, want %s", gotD, wantD)
	}
	if len(gotPairs) != len(wantPairs) {
		t.Fatalf("got %d pairs, want %d\ngot:  %v\nwant: %v",
			len(gotPairs), len(wantPairs), gotPairs, wantPairs)
	}
	for i := range wantPairs {
		if gotPairs[i] != wantPairs[i] {
			t.Fatalf("pair %d: got %v, want %v", i, gotPairs[i], wantPairs[i])
		}
	}
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 400; trial++ {
		n := 2 + rng.Intn(40)
		span := int64(1 + rng.Intn(20)) // small spans force many ties
		if trial%5 == 0 {
			span = 1 << 40 // sparse, large coordinates
		}
		pts := make([]Point, n)
		for i := range pts {
			pts[i] = Point{
				ID: int64(i + 1),
				X:  rng.Int63n(span) - span/2,
				Y:  rng.Int63n(span) - span/2,
			}
		}
		checkAgainstBruteForce(t, pts)
	}
}

// TestLargerRandom cross-checks a bigger instance where the
// divide-and-conquer recursion and strip logic are fully exercised.
func TestLargerRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	pts := make([]Point, 400)
	for i := range pts {
		pts[i] = Point{
			ID: int64(i + 1),
			X:  rng.Int63n(2000) - 1000,
			Y:  rng.Int63n(2000) - 1000,
		}
	}
	checkAgainstBruteForce(t, pts)
}

// TestMidlineStrip places the unique closest pair across the recursion
// split line so only the strip scan can find it.
func TestMidlineStrip(t *testing.T) {
	pts := []Point{
		{ID: 1, X: 0, Y: 0},
		{ID: 2, X: 10, Y: 0},
		{ID: 3, X: 11, Y: 0}, // crosses the midline with ID 2
		{ID: 4, X: 21, Y: 0},
	}
	d, pairs := fetchAll(t, pts)
	if d.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("Dist2=%s, want 1", d)
	}
	if len(pairs) != 1 || pairs[0] != (Pair{A: 2, B: 3}) {
		t.Fatalf("pairs=%v, want [{2 3}]", pairs)
	}
	checkAgainstBruteForce(t, pts)
}

// TestCoCircle puts 20 lattice points on the circle x^2+y^2=25^2,
// producing many tied closest pairs at squared distance 50.
func TestCoCircle(t *testing.T) {
	var pts []Point
	id := int64(1)
	add := func(x, y int64) {
		pts = append(pts, Point{ID: id, X: x, Y: y})
		id++
	}
	for _, sx := range []int64{1, -1} {
		for _, sy := range []int64{1, -1} {
			add(sx*7, sy*24)
			add(sx*24, sy*7)
			add(sx*15, sy*20)
			add(sx*20, sy*15)
		}
	}
	add(25, 0)
	add(-25, 0)
	add(0, 25)
	add(0, -25)

	d, pairs := fetchAll(t, pts)
	if d.Cmp(big.NewInt(50)) != 0 {
		t.Fatalf("Dist2=%s, want 50", d)
	}
	if len(pairs) != 12 {
		t.Fatalf("got %d tied pairs, want 12: %v", len(pairs), pairs)
	}
	checkAgainstBruteForce(t, pts)
}

// TestDuplicateGroups exercises the d=0 path with several clusters of
// coincident points, including one large quadratic group.
func TestDuplicateGroups(t *testing.T) {
	var pts []Point
	id := int64(1)
	add := func(x, y int64, k int) {
		for ; k > 0; k-- {
			pts = append(pts, Point{ID: id, X: x, Y: y})
			id++
		}
	}
	add(0, 0, 3)   // C(3,2)=3 pairs
	add(10, 10, 2) // C(2,2)=1 pair
	add(-5, 3, 1)  // singleton
	add(7, -9, 40) // C(40,2)=780 pairs
	add(-100, -100, 1)

	d, pairs := fetchAll(t, pts)
	if d.Sign() != 0 {
		t.Fatalf("Dist2=%s, want 0", d)
	}
	if len(pairs) != 784 {
		t.Fatalf("got %d pairs, want 784", len(pairs))
	}
	checkAgainstBruteForce(t, pts)
}

// TestPagination walks the result in small pages and verifies the
// concatenation equals the single-shot result with no gaps or repeats.
func TestPagination(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var pts []Point
	for i := 0; i < 60; i++ {
		pts = append(pts, Point{
			ID: int64(i + 1),
			X:  rng.Int63n(6) - 3,
			Y:  rng.Int63n(6) - 3,
		})
	}
	full, err := ClosestPairs(pts, 0, math.MaxInt32)
	if err != nil {
		t.Fatal(err)
	}
	if full.Total < 2 {
		t.Fatalf("test setup produced too few ties: %d", full.Total)
	}

	const pageSize = 7
	var collected []Pair
	for offset := 0; ; offset += pageSize {
		res, err := ClosestPairs(pts, offset, pageSize)
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != full.Total {
			t.Fatalf("Total changed between pages: %d vs %d", res.Total, full.Total)
		}
		if res.Dist2.Cmp(full.Dist2) != 0 {
			t.Fatalf("Dist2 changed between pages")
		}
		if len(res.Pairs) == 0 {
			break
		}
		if len(res.Pairs) > pageSize {
			t.Fatalf("page larger than limit: %d", len(res.Pairs))
		}
		collected = append(collected, res.Pairs...)
	}
	if len(collected) != full.Total {
		t.Fatalf("paginated %d pairs, want %d", len(collected), full.Total)
	}
	for i := range collected {
		if collected[i] != full.Pairs[i] {
			t.Fatalf("pair %d: paginated %v, full %v", i, collected[i], full.Pairs[i])
		}
	}

	// limit == 0 returns only the count.
	res, err := ClosestPairs(pts, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != full.Total || len(res.Pairs) != 0 {
		t.Fatalf("limit=0: Total=%d pairs=%d", res.Total, len(res.Pairs))
	}
	// Offset beyond the end yields an empty page, not an error.
	res, err = ClosestPairs(pts, full.Total+100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pairs) != 0 || res.Total != full.Total {
		t.Fatalf("out-of-range page: %+v", res)
	}
	// Sortedness of the full listing.
	for i := 1; i < len(full.Pairs); i++ {
		a, b := full.Pairs[i-1], full.Pairs[i]
		if a.A > b.A || (a.A == b.A && a.B >= b.B) {
			t.Fatalf("pairs not sorted at %d: %v then %v", i, a, b)
		}
		if a.A >= a.B {
			t.Fatalf("pair not normalized: %v", a)
		}
	}
}

// TestOverflow uses coordinates at the int64 extremes: the squared
// distances exceed int64 and even uint64, so only exact big-integer
// arithmetic (and no float64 square roots) gets them right.
func TestOverflow(t *testing.T) {
	pts := []Point{
		{ID: 1, X: math.MinInt64, Y: math.MinInt64},
		{ID: 2, X: math.MaxInt64, Y: math.MaxInt64},
		{ID: 3, X: math.MaxInt64, Y: math.MinInt64},
		{ID: 4, X: 0, Y: 0},
	}
	d, pairs := fetchAll(t, pts)

	// Reference computed directly with big.Int.
	ref := func(a, b Point) *big.Int {
		dx := new(big.Int).Sub(big.NewInt(a.X), big.NewInt(b.X))
		dy := new(big.Int).Sub(big.NewInt(a.Y), big.NewInt(b.Y))
		dx.Mul(dx, dx)
		dy.Mul(dy, dy)
		return dx.Add(dx, dy)
	}
	// The closest pair is (Max,Max)-(0,0): dist2 = 2*(2^63-1)^2,
	// which still overflows int64.
	want := ref(pts[1], pts[3])
	if d.Cmp(want) != 0 {
		t.Fatalf("Dist2=%s, want %s", d, want)
	}
	if len(pairs) != 1 || pairs[0] != (Pair{A: 2, B: 4}) {
		t.Fatalf("pairs=%v, want [{2 4}]", pairs)
	}
	// Sanity: the value genuinely overflows int64.
	if want.IsInt64() {
		t.Fatal("test distance unexpectedly fits in int64")
	}
}

func TestErrors(t *testing.T) {
	if _, err := ClosestPairs(nil, 0, 1); err != ErrTooFewPoints {
		t.Fatalf("nil input: %v", err)
	}
	if _, err := ClosestPairs([]Point{{ID: 1}}, 0, 1); err != ErrTooFewPoints {
		t.Fatalf("single point: %v", err)
	}
	dup := []Point{{ID: 1, X: 0, Y: 0}, {ID: 1, X: 5, Y: 5}}
	if _, err := ClosestPairs(dup, 0, 1); err != ErrDuplicateID {
		t.Fatalf("duplicate IDs: %v", err)
	}
	ok := []Point{{ID: 1, X: 0, Y: 0}, {ID: 2, X: 5, Y: 5}}
	if _, err := ClosestPairs(ok, -1, 1); err != ErrBadRange {
		t.Fatalf("negative offset: %v", err)
	}
	if _, err := ClosestPairs(ok, 0, -1); err != ErrBadRange {
		t.Fatalf("negative limit: %v", err)
	}
}
