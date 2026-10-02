package rtree

import (
	"math"
	"math/rand"
	"testing"
)

type shop struct {
	id  int
	lng float64
	lat float64
}

// bruteForce is the ground truth: linear scan over all shops.
func bruteForce(shops []shop, q Rect) map[int]bool {
	want := make(map[int]bool)
	for _, s := range shops {
		if PointRect(s.lng, s.lat).Intersects(q) {
			want[s.id] = true
		}
	}
	return want
}

func resultSet(results []interface{}) map[int]bool {
	got := make(map[int]bool, len(results))
	for _, r := range results {
		got[r.(shop).id] = true
	}
	return got
}

func insertShops(t *RTree, shops []shop) {
	for _, s := range shops {
		t.Insert(PointRect(s.lng, s.lat), s)
	}
}

func randomShops(rng *rand.Rand, n int) []shop {
	shops := make([]shop, n)
	for i := range shops {
		// Clustered around a few "city centers" to be realistic.
		cx := []float64{121.47, 116.40, 113.26, 104.06}[rng.Intn(4)]
		cy := []float64{31.23, 39.90, 23.13, 30.57}[rng.Intn(4)]
		shops[i] = shop{
			id:  i,
			lng: cx + rng.NormFloat64()*0.3,
			lat: cy + rng.NormFloat64()*0.3,
		}
	}
	return shops
}

func TestEmptyTree(t *testing.T) {
	tr := New(8)
	if tr.Size() != 0 || tr.Height() != 0 {
		t.Fatalf("empty tree: size=%d height=%d", tr.Size(), tr.Height())
	}
	if got := tr.Search(Rect{0, 0, 1, 1}); len(got) != 0 {
		t.Fatalf("empty tree search returned %d results", len(got))
	}
	if _, ok := tr.BoundingBox(); ok {
		t.Fatal("empty tree should report no bounding box")
	}
}

func TestInsertAndWindowQuery(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 10000
	shops := randomShops(rng, n)

	tr := New(16)
	insertShops(tr, shops)

	if tr.Size() != n {
		t.Fatalf("size = %d, want %d", tr.Size(), n)
	}

	// 200 random window queries of varying sizes, checked against brute force.
	for i := 0; i < 200; i++ {
		x := 103 + rng.Float64()*20
		y := 22 + rng.Float64()*19
		w := rng.Float64() * rng.Float64() * 2
		h := rng.Float64() * rng.Float64() * 2
		q := Rect{MinX: x, MinY: y, MaxX: x + w, MaxY: y + h}

		want := bruteForce(shops, q)
		got := resultSet(tr.Search(q))

		if len(got) != len(want) {
			t.Fatalf("query %d %+v: got %d results, want %d", i, q, len(got), len(want))
		}
		for id := range want {
			if !got[id] {
				t.Fatalf("query %d %+v: missing shop %d", i, q, id)
			}
		}
	}
}

func TestPolygonMBRInsert(t *testing.T) {
	// Insert minimum bounding rectangles of polygons, not just points.
	tr := New(8)
	type poly struct {
		name string
		mbr  Rect
	}
	polys := []poly{
		{"A", Rect{0, 0, 2, 2}},
		{"B", Rect{1, 1, 3, 3}},
		{"C", Rect{10, 10, 11, 11}},
		{"D", Rect{-5, -5, -4, -4}},
	}
	for _, p := range polys {
		tr.Insert(p.mbr, p.name)
	}

	got := tr.Search(Rect{1.5, 1.5, 1.6, 1.6})
	if len(got) != 2 {
		t.Fatalf("expected A and B, got %v", got)
	}
	got = tr.Search(Rect{9, 9, 12, 12})
	if len(got) != 1 || got[0].(string) != "C" {
		t.Fatalf("expected only C, got %v", got)
	}
	got = tr.Search(Rect{100, 100, 200, 200})
	if len(got) != 0 {
		t.Fatalf("expected no results, got %v", got)
	}
}

func TestPruning(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 20000
	shops := randomShops(rng, n)
	tr := New(16)
	insertShops(tr, shops)

	// A small viewport around one city center.
	q := Rect{MinX: 121.4, MinY: 31.2, MaxX: 121.55, MaxY: 31.3}
	results, visited := tr.SearchWithStats(q)

	want := bruteForce(shops, q)
	if len(results) != len(want) {
		t.Fatalf("got %d results, want %d", len(results), len(want))
	}

	total := tr.TotalNodes()
	t.Logf("visited %d of %d nodes (%.1f%% pruned), %d results",
		visited, total, 100*float64(total-visited)/float64(total), len(results))
	if visited >= total {
		t.Fatalf("no pruning happened: visited %d of %d nodes", visited, total)
	}
	if float64(visited) > 0.5*float64(total) {
		t.Fatalf("weak pruning: visited %d of %d nodes", visited, total)
	}
}

// TestSortedInsertNoDegeneration inserts points in x-sorted order — the
// classic worst case that degenerates naive R-Trees — and verifies the
// tree stays shallow and balanced thanks to reinsertion + quadratic split.
func TestSortedInsertNoDegeneration(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	const n = 20000
	shops := randomShops(rng, n)
	// Sort by longitude: adversarial insertion order.
	for i := 1; i < n; i++ {
		for j := i; j > 0 && shops[j-1].lng > shops[j].lng; j-- {
			shops[j-1], shops[j] = shops[j], shops[j-1]
		}
	}

	tr := New(16)
	insertShops(tr, shops)

	// A balanced tree with maxEntries=16 over 20000 entries should have
	// height ~3 (16^3 = 4096 leaves capacity). Allow generous slack.
	if tr.Height() > 5 {
		t.Fatalf("tree degenerated: height = %d", tr.Height())
	}

	// Every node (except the root) must be at least ~40% full, which
	// reinsertion/splitting guarantees; a degenerate tree would violate it.
	checkMinFill(t, tr.root, tr.minEntries, true)

	// Queries must still be exact.
	for i := 0; i < 50; i++ {
		x := 103 + rng.Float64()*20
		y := 22 + rng.Float64()*19
		q := Rect{MinX: x, MinY: y, MaxX: x + 0.5, MaxY: y + 0.5}
		want := bruteForce(shops, q)
		got := resultSet(tr.Search(q))
		if len(got) != len(want) {
			t.Fatalf("query %d: got %d, want %d", i, len(got), len(want))
		}
	}
	t.Logf("sorted insert: height=%d nodes=%d", tr.Height(), tr.TotalNodes())
}

func checkMinFill(t *testing.T, n *node, minEntries int, isRoot bool) {
	t.Helper()
	if !isRoot && len(n.entries) < minEntries {
		t.Fatalf("node underfilled: %d entries, min %d", len(n.entries), minEntries)
	}
	for _, e := range n.entries {
		if e.child != nil {
			checkMinFill(t, e.child, minEntries, false)
		}
	}
}

// TestReinsertionReducesOverlap compares overlap with reinsertion enabled
// vs. a tree that always splits, showing reinsertion curbs overlap.
func TestReinsertionReducesOverlap(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	const n = 8000
	shops := randomShops(rng, n)

	withReinsert := New(16)
	insertShops(withReinsert, shops)

	// Same tree, reinsertion disabled (p = 0 forces immediate split).
	alwaysSplit := New(16)
	alwaysSplit.reinsertP = 0
	// p=0 would make removeDistant keep everything; guard by using a
	// pre-marked map so overflow always splits instead.
	insertAlwaysSplit(alwaysSplit, shops)

	ovWith := totalOverlap(withReinsert.root)
	ovWithout := totalOverlap(alwaysSplit.root)
	t.Logf("internal-node overlap area: with reinsertion=%.4f, always split=%.4f",
		ovWith, ovWithout)
	if ovWith >= ovWithout {
		t.Fatalf("reinsertion did not reduce overlap: %.4f >= %.4f", ovWith, ovWithout)
	}
}

// insertAlwaysSplit inserts with reinsertion disabled by pre-marking every
// level as already reinserted.
func insertAlwaysSplit(tr *RTree, shops []shop) {
	for _, s := range shops {
		reinserted := map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true, 5: true}
		if split := tr.insertRec(tr.root, &entry{rect: PointRect(s.lng, s.lat), data: s}, 0, tr.height, reinserted); split != nil {
			tr.growRoot(split)
		}
		tr.size++
	}
}

// totalOverlap sums pairwise bbox overlap among siblings at internal nodes.
func totalOverlap(n *node) float64 {
	if n.leaf {
		return 0
	}
	total := 0.0
	for i := 0; i < len(n.entries); i++ {
		for j := i + 1; j < len(n.entries); j++ {
			a, b := n.entries[i].rect, n.entries[j].rect
			if a.Intersects(b) {
				w := math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX)
				h := math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
				total += w * h
			}
		}
	}
	for _, e := range n.entries {
		total += totalOverlap(e.child)
	}
	return total
}

func TestBoundingBox(t *testing.T) {
	tr := New(8)
	tr.Insert(PointRect(1, 2), "a")
	tr.Insert(PointRect(-3, 5), "b")
	tr.Insert(Rect{0, 0, 4, 1}, "c")
	bb, ok := tr.BoundingBox()
	if !ok {
		t.Fatal("expected bounding box")
	}
	want := Rect{MinX: -3, MinY: 0, MaxX: 4, MaxY: 5}
	if bb != want {
		t.Fatalf("bounding box = %+v, want %+v", bb, want)
	}
}

func BenchmarkInsert10k(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	shops := randomShops(rng, 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr := New(16)
		insertShops(tr, shops)
	}
}

func BenchmarkWindowQuery(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	shops := randomShops(rng, 10000)
	tr := New(16)
	insertShops(tr, shops)
	q := Rect{MinX: 121.3, MinY: 31.1, MaxX: 121.6, MaxY: 31.4}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Search(q)
	}
}
