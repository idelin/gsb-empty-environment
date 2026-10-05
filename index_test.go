package sparse2d

import (
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
)

// brute is the reference full-scan implementation used to cross-check
// the index. The sum is accumulated in a big.Int so overflow and the
// wrapped int64 value can be derived exactly.
func brute(points map[string]Point, x0, y0, x1, y1 int64) Result {
	var res Result
	if x1 <= x0 || y1 <= y0 {
		return res
	}
	acc := new(big.Int)
	for _, p := range points {
		if p.X >= x0 && p.X < x1 && p.Y >= y0 && p.Y < y1 {
			res.Count++
			acc.Add(acc, big.NewInt(p.Weight))
		}
	}
	if acc.IsInt64() {
		res.Sum = acc.Int64()
		return res
	}
	res.Overflow = true
	// wrap modulo 2^64 into the int64 range
	mod := new(big.Int).Lsh(big.NewInt(1), 64)
	m := new(big.Int).Mod(acc, mod)
	if m.Cmp(new(big.Int).Lsh(big.NewInt(1), 63)) >= 0 {
		m.Sub(m, mod)
	}
	res.Sum = m.Int64()
	return res
}

func checkQuery(t *testing.T, ix *Index, pts map[string]Point, x0, y0, x1, y1 int64) {
	t.Helper()
	got := ix.Query(x0, y0, x1, y1)
	want := brute(pts, x0, y0, x1, y1)
	if got != want {
		t.Fatalf("Query(%d,%d,%d,%d) = %+v, brute force = %+v",
			x0, y0, x1, y1, got, want)
	}
}

func TestHalfOpenBoundaries(t *testing.T) {
	ix := New()
	pts := map[string]Point{}
	add := func(id string, x, y, w int64) {
		if !ix.Add(id, x, y, w) {
			t.Fatalf("Add(%s) failed", id)
		}
		pts[id] = Point{x, y, w}
	}
	// points on and around the rectangle [10,20) x [100,200)
	add("inside", 15, 150, 1)
	add("edge-lo", 10, 100, 10) // on lower edges: inside
	add("edge-hi-x", 20, 150, 100)
	add("edge-hi-y", 15, 200, 1000)
	add("corner-hi", 20, 200, 10000)
	add("outside", 9, 150, 100000)

	r := ix.Query(10, 100, 20, 200)
	if r.Count != 2 || r.Sum != 11 || r.Overflow {
		t.Fatalf("half-open boundary: got %+v, want {Count:2 Sum:11}", r)
	}
	// cross-check every rectangle on the coordinate grid against full scan
	for _, x0 := range []int64{9, 10, 15, 20, 21} {
		for _, x1 := range []int64{9, 10, 15, 20, 21} {
			for _, y0 := range []int64{99, 100, 150, 200, 201} {
				for _, y1 := range []int64{99, 100, 150, 200, 201} {
					checkQuery(t, ix, pts, x0, y0, x1, y1)
				}
			}
		}
	}
}

func TestDuplicateCoordinatesDistinctIDs(t *testing.T) {
	ix := New()
	for i := 0; i < 5; i++ {
		if !ix.Add(fmt.Sprintf("p%d", i), 7, 7, int64(i+1)) {
			t.Fatalf("Add p%d failed", i)
		}
	}
	r := ix.Query(7, 7, 8, 8)
	if r.Count != 5 || r.Sum != 15 {
		t.Fatalf("duplicate coords: got %+v, want {Count:5 Sum:15}", r)
	}
	ix.Remove("p2")
	r = ix.Query(7, 7, 8, 8)
	if r.Count != 4 || r.Sum != 12 {
		t.Fatalf("after remove: got %+v, want {Count:4 Sum:12}", r)
	}
	if ix.DistinctX() != 1 {
		t.Fatalf("DistinctX = %d, want 1", ix.DistinctX())
	}
}

func TestNegativeWeights(t *testing.T) {
	ix := New()
	pts := map[string]Point{}
	for i, w := range []int64{-5, 3, -2, 0, -100, 42} {
		id := fmt.Sprintf("n%d", i)
		ix.Add(id, int64(i), int64(-i), w)
		pts[id] = Point{int64(i), int64(-i), w}
	}
	checkQuery(t, ix, pts, math.MinInt64, math.MinInt64, math.MaxInt64, math.MaxInt64)
	checkQuery(t, ix, pts, 1, -5, 5, 0)
	r := ix.Query(math.MinInt64, math.MinInt64, math.MaxInt64, math.MaxInt64)
	if r.Sum != -62 || r.Count != 6 || r.Overflow {
		t.Fatalf("negative weights: got %+v, want {Count:6 Sum:-62}", r)
	}
}

func TestExtremeSparseCoordinates(t *testing.T) {
	ix := New()
	pts := map[string]Point{}
	coords := []Point{
		{math.MinInt64, math.MinInt64, 1},
		{math.MinInt64 + 1, math.MaxInt64 - 1, -2},
		{math.MaxInt64 - 1, math.MinInt64 + 1, 3},
		{math.MaxInt64 - 2, math.MaxInt64 - 2, -4},
		{0, 0, 5},
		{1 << 60, -(1 << 60), 6},
	}
	for i, p := range coords {
		id := fmt.Sprintf("e%d", i)
		if !ix.Add(id, p.X, p.Y, p.Weight) {
			t.Fatalf("Add %s failed", id)
		}
		pts[id] = p
	}
	// full-range query (note: MaxInt64 itself is not coverable, so the
	// test points stay one below it)
	checkQuery(t, ix, pts, math.MinInt64, math.MinInt64, math.MaxInt64, math.MaxInt64)
	// degenerate and single-column rectangles around the extremes
	for _, p := range coords {
		checkQuery(t, ix, pts, p.X, p.Y, p.X+1, p.Y+1)
		checkQuery(t, ix, pts, p.X, p.Y, p.X, p.Y) // empty rectangle
		checkQuery(t, ix, pts, p.X-1, p.Y-1, p.X, p.Y)
	}
	checkQuery(t, ix, pts, math.MinInt64, math.MinInt64, math.MinInt64+2, math.MaxInt64)
	checkQuery(t, ix, pts, -(1 << 61), -(1 << 61), 1<<61, 1<<61)
}

func TestReplaceAtomic(t *testing.T) {
	ix := New()
	pts := map[string]Point{}
	ix.Add("a", 1, 1, 10)
	ix.Add("b", 2, 2, 20)
	pts["a"] = Point{1, 1, 10}
	pts["b"] = Point{2, 2, 20}

	if ix.Replace("missing", 0, 0, 0) {
		t.Fatal("Replace of missing id should fail")
	}
	if !ix.Replace("a", 100, 100, -7) {
		t.Fatal("Replace failed")
	}
	pts["a"] = Point{100, 100, -7}

	// old position must no longer match, new position must match
	checkQuery(t, ix, pts, 0, 0, 10, 10)
	checkQuery(t, ix, pts, 99, 99, 101, 101)
	checkQuery(t, ix, pts, math.MinInt64, math.MinInt64, math.MaxInt64, math.MaxInt64)

	if got := ix.Stats().Replaces; got != 1 {
		t.Fatalf("Replaces = %d, want 1", got)
	}
}

func TestOverflow(t *testing.T) {
	ix := New()
	ix.Add("max1", 0, 0, math.MaxInt64)
	ix.Add("max2", 1, 1, math.MaxInt64)
	r := ix.Query(-1, -1, 2, 2)
	if !r.Overflow {
		t.Fatalf("expected overflow, got %+v", r)
	}
	if r.Sum != -2 { // 2*MaxInt64 wraps to -2
		t.Fatalf("wrapped sum = %d, want -2", r.Sum)
	}
	if r.Count != 2 {
		t.Fatalf("count = %d, want 2", r.Count)
	}

	// negative overflow
	ix2 := New()
	ix2.Add("min1", 0, 0, math.MinInt64)
	ix2.Add("min2", 1, 1, math.MinInt64)
	r = ix2.Query(-1, -1, 2, 2)
	if !r.Overflow || r.Sum != 0 {
		t.Fatalf("negative overflow: got %+v, want {Sum:0 Overflow:true}", r)
	}

	// a subtree partial sum may wrap while the exact total still fits:
	// MaxInt64 + 1 + (MinInt64+1) == 1, so no overflow is reported and
	// the wrapped sum is exact.
	ix3 := New()
	ix3.Add("a", 0, 0, math.MaxInt64)
	ix3.Add("b", 0, 1, 1) // same x: subtree partial sum wraps
	ix3.Add("c", 1, 0, math.MinInt64+1)
	r = ix3.Query(-1, -1, 2, 2)
	if r.Overflow || r.Sum != 1 {
		t.Fatalf("wrapped partial sum, exact total: got %+v, want {Sum:1}", r)
	}
}

func TestRemoveAndDistinctX(t *testing.T) {
	ix := New()
	ix.Add("a", 1, 1, 1)
	ix.Add("b", 1, 2, 2)
	ix.Add("c", 2, 1, 3)
	if ix.DistinctX() != 2 {
		t.Fatalf("DistinctX = %d, want 2", ix.DistinctX())
	}
	ix.Remove("c")
	if ix.DistinctX() != 1 {
		t.Fatalf("DistinctX after remove = %d, want 1", ix.DistinctX())
	}
	ix.Remove("a")
	ix.Remove("b")
	if ix.Len() != 0 || ix.DistinctX() != 0 {
		t.Fatalf("index not empty: Len=%d DistinctX=%d", ix.Len(), ix.DistinctX())
	}
	if ix.Remove("a") {
		t.Fatal("Remove of absent id should return false")
	}
	if ix.Add("x", 0, 0, 0) != true || ix.Add("x", 1, 1, 1) != false {
		t.Fatal("Add duplicate semantics broken")
	}
}

// TestFuzzVsBruteForce runs random operation sequences with duplicate
// coordinates, negative weights and huge sparse coordinates, verifying
// every query against a full scan with big.Int arithmetic.
func TestFuzzVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 7))
	ix := New()
	pts := map[string]Point{}

	coordPool := []int64{
		0, 1, -1, 5, -5, 100, -100,
		math.MinInt64, math.MinInt64 + 1, math.MaxInt64 - 1, math.MaxInt64 - 2,
		1 << 62, -(1 << 62), 1<<62 + 1,
	}
	randCoord := func() int64 {
		if rng.Uint64()%3 == 0 {
			return coordPool[rng.IntN(len(coordPool))]
		}
		return int64(rng.IntN(41)) - 20 // dense cluster -> many duplicates
	}
	randWeight := func() int64 {
		switch rng.IntN(4) {
		case 0:
			return -int64(rng.IntN(1000))
		case 1:
			return int64(rng.IntN(1000))
		case 2:
			return coordPool[rng.IntN(len(coordPool))] // huge weights
		default:
			return int64(rng.IntN(11)) - 5
		}
	}

	var ids []string
	for step := 0; step < 4000; step++ {
		switch op := rng.IntN(5); op {
		case 0, 1: // add
			id := fmt.Sprintf("id%d", step)
			p := Point{randCoord(), randCoord(), randWeight()}
			if ix.Add(id, p.X, p.Y, p.Weight) {
				pts[id] = p
				ids = append(ids, id)
			}
		case 2: // remove
			if len(ids) > 0 {
				i := rng.IntN(len(ids))
				id := ids[i]
				if _, ok := pts[id]; ok {
					if !ix.Remove(id) {
						t.Fatalf("Remove(%s) failed", id)
					}
					delete(pts, id)
				}
				ids = append(ids[:i], ids[i+1:]...)
			}
		case 3: // replace
			if len(ids) > 0 {
				id := ids[rng.IntN(len(ids))]
				if _, ok := pts[id]; ok {
					p := Point{randCoord(), randCoord(), randWeight()}
					if !ix.Replace(id, p.X, p.Y, p.Weight) {
						t.Fatalf("Replace(%s) failed", id)
					}
					pts[id] = p
				}
			}
		case 4: // query
			x0, x1 := randCoord(), randCoord()
			y0, y1 := randCoord(), randCoord()
			if rng.IntN(2) == 0 {
				x1 = x0 + int64(rng.IntN(10))
			}
			if rng.IntN(2) == 0 {
				y1 = y0 + int64(rng.IntN(10))
			}
			checkQuery(t, ix, pts, x0, y0, x1, y1)
		}
		if ix.Len() != len(pts) {
			t.Fatalf("step %d: Len=%d, want %d", step, ix.Len(), len(pts))
		}
	}
	// final sweep of full-range and boundary queries
	checkQuery(t, ix, pts, math.MinInt64, math.MinInt64, math.MaxInt64, math.MaxInt64)
	for _, x := range coordPool {
		for _, y := range coordPool {
			checkQuery(t, ix, pts, x, y, x+1, y+1)
		}
	}
	t.Logf("final: %d points, %d distinct x, stats=%+v",
		ix.Len(), ix.DistinctX(), ix.Stats())
}

func TestStats(t *testing.T) {
	ix := New()
	for i := 0; i < 1000; i++ {
		ix.Add(fmt.Sprintf("s%d", i), int64(i%17), int64(i), 1)
	}
	ix.ResetStats()
	r := ix.Query(0, 0, 17, 1000)
	if r.Count != 1000 {
		t.Fatalf("count = %d, want 1000", r.Count)
	}
	st := ix.Stats()
	if st.Queries != 1 || st.NodeVisits <= 0 {
		t.Fatalf("stats not recorded: %+v", st)
	}
	if st.Inserts != 0 {
		t.Fatalf("ResetStats did not clear inserts: %+v", st)
	}
	t.Logf("full-range query over 1000 points / 17 columns: %d node visits", st.NodeVisits)
}
