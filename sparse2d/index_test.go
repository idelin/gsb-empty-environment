package sparse2d

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func mustAdd(t *testing.T, ix *Index, p Point) {
	t.Helper()
	if err := ix.Add(p); err != nil {
		t.Fatalf("Add(%+v): %v", p, err)
	}
}

func mustQuery(t *testing.T, ix *Index, r Rect) Result {
	t.Helper()
	res, err := ix.Query(r)
	if err != nil {
		t.Fatalf("Query(%+v): %v", r, err)
	}
	return res
}

func TestHalfOpenBoundaries(t *testing.T) {
	ix := New()
	// Points on every edge and corner of rect [10,20) x [100,200).
	pts := []Point{
		{ID: "inside", X: 15, Y: 150, W: 1},
		{ID: "min-edge", X: 10, Y: 100, W: 2},    // included corner
		{ID: "max-x", X: 20, Y: 150, W: 4},       // excluded (half-open)
		{ID: "max-y", X: 15, Y: 200, W: 8},       // excluded (half-open)
		{ID: "max-corner", X: 20, Y: 200, W: 16}, // excluded
		{ID: "below-min-x", X: 9, Y: 150, W: 32},
		{ID: "below-min-y", X: 15, Y: 99, W: 64},
	}
	for _, p := range pts {
		mustAdd(t, ix, p)
	}
	res := mustQuery(t, ix, Rect{MinX: 10, MinY: 100, MaxX: 20, MaxY: 200})
	if res.Count != 2 || res.Sum != 3 || res.Overflow {
		t.Fatalf("got %+v, want Count=2 Sum=3 Overflow=false", res)
	}
	// Empty ranges are valid and match nothing.
	res = mustQuery(t, ix, Rect{MinX: 15, MinY: 150, MaxX: 15, MaxY: 150})
	if res.Count != 0 || res.Sum != 0 {
		t.Fatalf("empty rect: got %+v", res)
	}
	// Inverted ranges are rejected.
	if _, err := ix.Query(Rect{MinX: 20, MinY: 0, MaxX: 10, MaxY: 1}); err != ErrBadRect {
		t.Fatalf("inverted rect: got %v, want ErrBadRect", err)
	}
}

func TestNegativeWeights(t *testing.T) {
	ix := New()
	mustAdd(t, ix, Point{ID: "a", X: 0, Y: 0, W: -5})
	mustAdd(t, ix, Point{ID: "b", X: 1, Y: 1, W: -7})
	mustAdd(t, ix, Point{ID: "c", X: 2, Y: 2, W: 3})
	res := mustQuery(t, ix, Rect{MinX: -1, MinY: -1, MaxX: 3, MaxY: 3})
	if res.Count != 3 || res.Sum != -9 || res.Overflow {
		t.Fatalf("got %+v, want Count=3 Sum=-9", res)
	}
	res = mustQuery(t, ix, Rect{MinX: 0, MinY: 0, MaxX: 2, MaxY: 2})
	if res.Count != 2 || res.Sum != -12 {
		t.Fatalf("got %+v, want Count=2 Sum=-12", res)
	}
}

func TestHugeSparseCoordinates(t *testing.T) {
	ix := New()
	// Extremely sparse coordinates near the int64 limits. Note the rect
	// is half-open, so a point at MaxInt64 can only be excluded, never
	// included; use MaxInt64-1 as the largest includable coordinate.
	mustAdd(t, ix, Point{ID: "neg", X: math.MinInt64, Y: math.MinInt64, W: 11})
	mustAdd(t, ix, Point{ID: "pos", X: math.MaxInt64 - 1, Y: math.MaxInt64 - 1, W: 13})
	mustAdd(t, ix, Point{ID: "zero", X: 0, Y: 0, W: 17})
	mustAdd(t, ix, Point{ID: "edge", X: math.MaxInt64, Y: math.MaxInt64, W: 100})

	res := mustQuery(t, ix, Rect{MinX: math.MinInt64, MinY: math.MinInt64, MaxX: math.MaxInt64, MaxY: math.MaxInt64})
	if res.Count != 3 || res.Sum != 41 { // "edge" excluded by half-open bound
		t.Fatalf("got %+v, want Count=3 Sum=41", res)
	}
	res = mustQuery(t, ix, Rect{MinX: math.MinInt64, MinY: math.MinInt64, MaxX: math.MinInt64 + 1, MaxY: math.MinInt64 + 1})
	if res.Count != 1 || res.Sum != 11 {
		t.Fatalf("got %+v, want Count=1 Sum=11", res)
	}
	res = mustQuery(t, ix, Rect{MinX: math.MaxInt64 - 1, MinY: math.MaxInt64 - 1, MaxX: math.MaxInt64, MaxY: math.MaxInt64})
	if res.Count != 1 || res.Sum != 13 {
		t.Fatalf("got %+v, want Count=1 Sum=13", res)
	}
}

func TestDuplicateCoordinatesDistinctIDs(t *testing.T) {
	ix := New()
	mustAdd(t, ix, Point{ID: "p1", X: 5, Y: 5, W: 10})
	mustAdd(t, ix, Point{ID: "p2", X: 5, Y: 5, W: 20})
	mustAdd(t, ix, Point{ID: "p3", X: 5, Y: 5, W: -4})
	res := mustQuery(t, ix, Rect{MinX: 5, MinY: 5, MaxX: 6, MaxY: 6})
	if res.Count != 3 || res.Sum != 26 {
		t.Fatalf("got %+v, want Count=3 Sum=26", res)
	}
	// Removing one ID leaves the others billed separately.
	if err := ix.Remove("p2"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	res = mustQuery(t, ix, Rect{MinX: 5, MinY: 5, MaxX: 6, MaxY: 6})
	if res.Count != 2 || res.Sum != 6 {
		t.Fatalf("got %+v, want Count=2 Sum=6", res)
	}
	// IDs are stable and unique.
	if err := ix.Add(Point{ID: "p1", X: 9, Y: 9, W: 1}); err == nil {
		t.Fatal("Add with duplicate ID: want error")
	}
	if err := ix.Remove("p2"); err == nil {
		t.Fatal("Remove of missing ID: want error")
	}
}

func TestReplaceAtomic(t *testing.T) {
	ix := New()
	mustAdd(t, ix, Point{ID: "a", X: 1, Y: 1, W: 5})
	mustAdd(t, ix, Point{ID: "b", X: 2, Y: 2, W: 7})
	full := Rect{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}

	if err := ix.Replace(Point{ID: "a", X: 3, Y: 3, W: -2}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	// After the atomic swap the old version must be fully gone and the
	// new one fully present: no double-counting, no disappearance.
	res := mustQuery(t, ix, full)
	if res.Count != 2 || res.Sum != 5 {
		t.Fatalf("got %+v, want Count=2 Sum=5", res)
	}
	res = mustQuery(t, ix, Rect{MinX: 1, MinY: 1, MaxX: 2, MaxY: 2})
	if res.Count != 0 || res.Sum != 0 {
		t.Fatalf("stale point still visible: %+v", res)
	}
	res = mustQuery(t, ix, Rect{MinX: 3, MinY: 3, MaxX: 4, MaxY: 4})
	if res.Count != 1 || res.Sum != -2 {
		t.Fatalf("new point not visible: %+v", res)
	}
	// Replace requires an existing ID.
	if err := ix.Replace(Point{ID: "ghost", X: 0, Y: 0, W: 1}); err == nil {
		t.Fatal("Replace of missing ID: want error")
	}
	// Replacing with identical data is a no-op on results.
	if err := ix.Replace(Point{ID: "b", X: 2, Y: 2, W: 7}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	res = mustQuery(t, ix, full)
	if res.Count != 2 || res.Sum != 5 {
		t.Fatalf("got %+v, want Count=2 Sum=5", res)
	}
}

func TestOverflowSaturation(t *testing.T) {
	ix := New()
	mustAdd(t, ix, Point{ID: "big1", X: 0, Y: 0, W: math.MaxInt64})
	mustAdd(t, ix, Point{ID: "big2", X: 1, Y: 1, W: math.MaxInt64})
	mustAdd(t, ix, Point{ID: "small", X: 2, Y: 2, W: -100})
	full := Rect{MinX: -1, MinY: -1, MaxX: 3, MaxY: 3}

	res := mustQuery(t, ix, full)
	if !res.Overflow || res.Sum != math.MaxInt64 || res.Count != 3 {
		t.Fatalf("got %+v, want saturated MaxInt64 with Overflow", res)
	}
	// Cancellation inside the same rectangle brings the exact total back
	// into int64 range: no overflow may be reported.
	mustAdd(t, ix, Point{ID: "neg1", X: 3, Y: 3, W: math.MinInt64})
	mustAdd(t, ix, Point{ID: "neg2", X: 4, Y: 4, W: math.MinInt64})
	res = mustQuery(t, ix, Rect{MinX: -1, MinY: -1, MaxX: 5, MaxY: 5})
	if res.Overflow || res.Sum != -102 { // 2*(2^63-1) + 2*(-2^63) - 100 = -102
		t.Fatalf("got %+v, want Sum=-102 Overflow=false", res)
	}
	// Pure negative overflow saturates to MinInt64.
	res = mustQuery(t, ix, Rect{MinX: 3, MinY: 3, MaxX: 5, MaxY: 5})
	if !res.Overflow || res.Sum != math.MinInt64 {
		t.Fatalf("got %+v, want saturated MinInt64 with Overflow", res)
	}
	// Overflow detection survives deletions too.
	if err := ix.Remove("neg1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	res = mustQuery(t, ix, Rect{MinX: -1, MinY: -1, MaxX: 5, MaxY: 5})
	// exact: 2*(2^63-1) - 2^63 - 100 = 2^63 - 102 -> fits again
	if res.Overflow || res.Sum != math.MaxInt64-101 {
		t.Fatalf("got %+v, want Sum=MaxInt64-101 Overflow=false", res)
	}
	// Removing the second negative weight pushes the exact total over.
	if err := ix.Remove("neg2"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	res = mustQuery(t, ix, Rect{MinX: -1, MinY: -1, MaxX: 5, MaxY: 5})
	// exact: 2*(2^63-1) - 100 = 2^64 - 102 -> positive overflow
	if !res.Overflow || res.Sum != math.MaxInt64 {
		t.Fatalf("got %+v, want saturated MaxInt64 with Overflow", res)
	}
}

func TestRandomizedVsFullScan(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ix := New()
	coordPool := []int64{
		math.MinInt64, math.MinInt64 + 1, -1 << 40, -100, -1, 0, 1, 100,
		1 << 40, math.MaxInt64 - 1, math.MaxInt64,
	}
	randCoord := func() int64 {
		if rng.Intn(2) == 0 {
			return coordPool[rng.Intn(len(coordPool))]
		}
		return rng.Int63n(2000) - 1000
	}
	randWeight := func() int64 {
		switch rng.Intn(10) {
		case 0:
			return math.MaxInt64
		case 1:
			return math.MinInt64
		case 2:
			return -rng.Int63n(1000) - 1 // negative weights
		default:
			return rng.Int63n(1000)
		}
	}

	var live []string
	for op := 0; op < 4000; op++ {
		switch {
		case len(live) == 0 || rng.Intn(100) < 45:
			id := fmt.Sprintf("id-%d", op)
			p := Point{ID: id, X: randCoord(), Y: randCoord(), W: randWeight()}
			if err := ix.Add(p); err != nil {
				t.Fatalf("op %d Add: %v", op, err)
			}
			live = append(live, id)
		case rng.Intn(100) < 55:
			i := rng.Intn(len(live))
			id := live[i]
			live = append(live[:i], live[i+1:]...)
			if err := ix.Remove(id); err != nil {
				t.Fatalf("op %d Remove(%q): %v", op, id, err)
			}
		default:
			i := rng.Intn(len(live))
			p := Point{ID: live[i], X: randCoord(), Y: randCoord(), W: randWeight()}
			if err := ix.Replace(p); err != nil {
				t.Fatalf("op %d Replace(%q): %v", op, p.ID, err)
			}
		}

		if op%97 == 0 {
			x1, x2 := randCoord(), randCoord()
			y1, y2 := randCoord(), randCoord()
			if x1 > x2 {
				x1, x2 = x2, x1
			}
			if y1 > y2 {
				y1, y2 = y2, y1
			}
			r := Rect{MinX: x1, MinY: y1, MaxX: x2, MaxY: y2}
			got, err := ix.Query(r)
			if err != nil {
				t.Fatalf("op %d Query: %v", op, err)
			}
			want, err := ix.Scan(r)
			if err != nil {
				t.Fatalf("op %d Scan: %v", op, err)
			}
			if got.Count != want.Count || got.Sum != want.Sum || got.Overflow != want.Overflow {
				t.Fatalf("op %d rect %+v: Query=%+v Scan=%+v", op, r, got, want)
			}
		}
	}
	if ix.Len() != len(live) {
		t.Fatalf("Len=%d, want %d", ix.Len(), len(live))
	}
}

func TestAccessStats(t *testing.T) {
	ix := New()
	for i := 0; i < 100; i++ {
		mustAdd(t, ix, Point{ID: fmt.Sprintf("p%d", i), X: int64(i % 10), Y: int64(i), W: int64(i)})
	}
	ix.ResetStats()
	res := mustQuery(t, ix, Rect{MinX: 0, MinY: 0, MaxX: 10, MaxY: 100})
	if res.Stats.XNodesVisited == 0 || res.Stats.FenwickReads == 0 {
		t.Fatalf("query stats not recorded: %+v", res.Stats)
	}
	tot := ix.Stats()
	if tot.Queries != 1 || tot.XNodesVisited != res.Stats.XNodesVisited {
		t.Fatalf("totals not accumulated: %+v", tot)
	}
	mustAdd(t, ix, Point{ID: "new", X: 3, Y: 1000, W: 1})
	tot = ix.Stats()
	if tot.Mutations != 1 || tot.FenwickWrites == 0 || tot.SlotRebuilds == 0 {
		t.Fatalf("mutation stats not recorded: %+v", tot)
	}
	ix.ResetStats()
	if got := ix.Stats(); got != (Totals{}) {
		t.Fatalf("ResetStats: %+v", got)
	}
}
