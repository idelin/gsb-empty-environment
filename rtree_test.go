package rtree

import (
	"math/rand"
	"reflect"
	"testing"
)

func randRect(rng *rand.Rand, grid int64) Rect {
	x1 := rng.Int63n(grid)
	y1 := rng.Int63n(grid)
	var x2, y2 int64
	switch rng.Intn(4) {
	case 0: // point
		x2, y2 = x1, y1
	case 1: // horizontal/vertical line
		x2 = x1 + rng.Int63n(4)
		y2 = y1
	default: // box
		x2 = x1 + rng.Int63n(6)
		y2 = y1 + rng.Int63n(6)
	}
	return NewRect(x1, y1, x2, y2)
}

func modelQuery(model map[int64]Rect, q Rect) []int64 {
	ids := []int64{}
	for id, r := range model {
		if r.Intersects(q) {
			ids = append(ids, id)
		}
	}
	for i := 0; i+1 < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids
}

// TestRandomAgainstModel mutates the tree randomly on a small integer grid
// and cross-checks every query against a full scan of a reference model.
func TestRandomAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	tr, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	model := map[int64]Rect{}
	var live []int64
	nextID := int64(1)
	for step := 0; step < 4000; step++ {
		op := rng.Intn(10)
		switch {
		case op < 4 || len(live) == 0:
			id := nextID
			nextID++
			r := randRect(rng, 24)
			if err := tr.Insert(id, r); err != nil {
				t.Fatalf("step %d insert: %v", step, err)
			}
			model[id] = r
			live = append(live, id)
		case op < 6:
			id := live[rng.Intn(len(live))]
			r := randRect(rng, 24)
			if err := tr.Replace(id, r); err != nil {
				t.Fatalf("step %d replace: %v", step, err)
			}
			model[id] = r
		case op < 8:
			i := rng.Intn(len(live))
			id := live[i]
			live = append(live[:i], live[i+1:]...)
			if err := tr.Delete(id); err != nil {
				t.Fatalf("step %d delete: %v", step, err)
			}
			delete(model, id)
		default:
			q := randRect(rng, 24)
			got, _ := tr.RangeQuery(q)
			want := modelQuery(model, q)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d query %+v: got %v, want %v", step, q, got, want)
			}
		}
		if tr.Size() != len(model) {
			t.Fatalf("step %d: size %d, model %d", step, tr.Size(), len(model))
		}
		if step%25 == 0 {
			if err := tr.Validate(); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
		}
	}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestBoundaryTouch verifies closed-interval intersection on edges/corners.
func TestBoundaryTouch(t *testing.T) {
	tr, _ := New(4)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tr.Insert(1, NewRect(0, 0, 5, 5)))     // box
	must(tr.Insert(2, NewRect(10, 0, 10, 8)))   // vertical line
	must(tr.Insert(3, NewRect(-4, -4, -4, -4))) // point
	cases := []struct {
		q    Rect
		want []int64
	}{
		{NewRect(5, 5, 9, 9), []int64{1}},      // corner touch
		{NewRect(5, -2, 7, 3), []int64{1}},     // edge touch
		{NewRect(6, 0, 9, 9), []int64{}},       // one past the edge
		{NewRect(10, 8, 10, 8), []int64{2}},    // line endpoint touch
		{NewRect(9, 0, 9, 8), []int64{}},       // parallel miss
		{NewRect(-4, -4, -4, -4), []int64{3}},  // exact point
		{NewRect(-4, -4, 5, 5), []int64{1, 3}}, // spans point and box
		{NewRect(-5, -5, -5, -5), []int64{}},   // next to the point
	}
	for _, c := range cases {
		got, _ := tr.RangeQuery(c.q)
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("query %+v: got %v, want %v", c.q, got, c.want)
		}
	}
}

// TestIdenticalRects inserts many copies of the same rectangle, then
// deletes them all; no ghosts may remain.
func TestIdenticalRects(t *testing.T) {
	tr, _ := New(4)
	r := NewRect(3, 3, 7, 7)
	const n = 200
	for id := int64(1); id <= n; id++ {
		if err := tr.Insert(id, r); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := tr.RangeQuery(r)
	if len(got) != n {
		t.Fatalf("got %d ids, want %d", len(got), n)
	}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= n; id++ {
		if err := tr.Delete(id); err != nil {
			t.Fatal(err)
		}
	}
	if tr.Size() != 0 || tr.Height() != 1 {
		t.Fatalf("after draining: size=%d height=%d", tr.Size(), tr.Height())
	}
	got, _ = tr.RangeQuery(r)
	if len(got) != 0 {
		t.Fatalf("ghost objects remain: %v", got)
	}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestRootSplitShrinkCycles repeatedly grows and drains the tree to
// exercise root splits and root contraction.
func TestRootSplitShrinkCycles(t *testing.T) {
	tr, _ := New(4)
	for cycle := 0; cycle < 4; cycle++ {
		for id := int64(1); id <= 300; id++ {
			if err := tr.Insert(id, NewRect(id%17, id%13, id%17, id%13)); err != nil {
				t.Fatal(err)
			}
		}
		if tr.Height() < 3 {
			t.Fatalf("cycle %d: expected height >= 3, got %d", cycle, tr.Height())
		}
		if err := tr.Validate(); err != nil {
			t.Fatalf("cycle %d after inserts: %v", cycle, err)
		}
		for id := int64(1); id <= 300; id++ {
			if err := tr.Delete(id); err != nil {
				t.Fatal(err)
			}
		}
		if tr.Height() != 1 || tr.Size() != 0 {
			t.Fatalf("cycle %d after deletes: height=%d size=%d", cycle, tr.Height(), tr.Size())
		}
		if err := tr.Validate(); err != nil {
			t.Fatalf("cycle %d after deletes: %v", cycle, err)
		}
	}
}

// TestReplaceRollback verifies that a failed replace leaves the tree
// completely untouched, and that a successful replace is atomic.
func TestReplaceRollback(t *testing.T) {
	tr, _ := New(4)
	for id := int64(1); id <= 50; id++ {
		if err := tr.Insert(id, NewRect(id, 0, id, 5)); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := tr.RangeQuery(NewRect(-100, -100, 100, 100))
	if err := tr.Replace(999, NewRect(0, 0, 1, 1)); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	after, _ := tr.RangeQuery(NewRect(-100, -100, 100, 100))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed replace mutated the tree: %v -> %v", before, after)
	}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
	// Successful replace: old location must not answer, new one must.
	if err := tr.Replace(7, NewRect(1000, 1000, 1001, 1001)); err != nil {
		t.Fatal(err)
	}
	if got, _ := tr.RangeQuery(NewRect(7, 0, 7, 5)); len(got) != 0 {
		t.Fatalf("stale entry at old location: %v", got)
	}
	if got, _ := tr.RangeQuery(NewRect(1000, 1000, 1001, 1001)); !reflect.DeepEqual(got, []int64{7}) {
		t.Fatalf("new location: got %v", got)
	}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
	// Duplicate insert must fail without side effects.
	if err := tr.Insert(7, NewRect(0, 0, 0, 0)); err != ErrDuplicate {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
	if err := tr.Delete(4242); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestPruningVisitedNodes shows that queries visit far fewer nodes than
// the tree contains, i.e. bounding-box pruning is actually happening.
func TestPruningVisitedNodes(t *testing.T) {
	tr, _ := New(8)
	id := int64(1)
	const side = 40
	for x := int64(0); x < side; x++ {
		for y := int64(0); y < side; y++ {
			if err := tr.Insert(id, NewRect(x*4, y*4, x*4+1, y*4+1)); err != nil {
				t.Fatal(err)
			}
			id++
		}
	}
	total := countNodes(tr.root)
	got, visited := tr.RangeQuery(NewRect(0, 0, 15, 15))
	if len(got) != 16 {
		t.Fatalf("got %d ids, want 16", len(got))
	}
	t.Logf("visited %d of %d nodes (%.1f%%)", visited, total, 100*float64(visited)/float64(total))
	if visited >= total {
		t.Fatalf("no pruning: visited %d of %d nodes", visited, total)
	}
	if visited > total/3 {
		t.Fatalf("weak pruning: visited %d of %d nodes", visited, total)
	}
	// A full-coverage query visits everything and returns everything.
	all, visitedAll := tr.RangeQuery(NewRect(-1, -1, 4*side, 4*side))
	if len(all) != side*side {
		t.Fatalf("full query got %d ids", len(all))
	}
	if visitedAll != total {
		t.Fatalf("full query visited %d of %d nodes", visitedAll, total)
	}
}

func countNodes(n *node) int {
	c := 1
	for _, e := range n.entries {
		if e.child != nil {
			c += countNodes(e.child)
		}
	}
	return c
}

// TestValidatorCatchesCorruption sanity-checks that the validator actually
// detects structural violations.
func TestValidatorCatchesCorruption(t *testing.T) {
	tr, _ := New(4)
	for id := int64(1); id <= 30; id++ {
		if err := tr.Insert(id, NewRect(id, id, id+1, id+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
	// Corrupt one internal MBR and expect the validator to notice.
	var corrupt func(n *node) bool
	corrupt = func(n *node) bool {
		for i := range n.entries {
			if n.entries[i].child != nil && n.entries[i].child.leaf() {
				n.entries[i].rect.MaxX++
				return true
			}
			if n.entries[i].child != nil && corrupt(n.entries[i].child) {
				return true
			}
		}
		return false
	}
	if !corrupt(tr.root) {
		t.Fatal("no internal entry to corrupt")
	}
	if err := tr.Validate(); err == nil {
		t.Fatal("validator accepted a corrupted tree")
	}
}
