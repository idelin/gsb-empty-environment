package bboxindex

import (
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// bruteForce is the reference implementation: a full scan over the
// ground-truth model.
func bruteForce(model map[uint64]Box, q Box) []uint64 {
	if q.Empty() {
		return nil
	}
	var out []uint64
	for id, b := range model {
		if b.Intersects(q) {
			out = append(out, id)
		}
	}
	return out
}

func sorted(ids []uint64) []uint64 {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func checkQuery(t *testing.T, idx *Index, model map[uint64]Box, q Box) {
	t.Helper()
	got := sorted(idx.Query(q))
	want := sorted(bruteForce(model, q))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query %+v: got %v, want %v", q, got, want)
	}
}

// TestQueryMatchesFullScan hammers the index with random moves, deletes
// and rebuilds, verifying every query against a full scan of the model.
func TestQueryMatchesFullScan(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	idx := New()
	model := make(map[uint64]Box)

	randBox := func() Box {
		x1, y1 := rng.Float64()*100, rng.Float64()*100
		x2, y2 := rng.Float64()*100, rng.Float64()*100
		if x1 > x2 {
			x1, x2 = x2, x1
		}
		if y1 > y2 {
			y1, y2 = y2, y1
		}
		return Box{MinX: x1, MinY: y1, MaxX: x2, MaxY: y2}
	}

	var genBefore uint64
	for op := 0; op < 3000; op++ {
		id := uint64(rng.Intn(200))
		switch rng.Intn(10) {
		case 0, 1: // delete
			idx.Delete(id)
			delete(model, id)
		case 2: // empty (inverted) box upsert
			b := Box{MinX: 10, MinY: 10, MaxX: 5, MaxY: 5}
			idx.Upsert(id, b)
			model[id] = b
		default: // move / insert
			b := randBox()
			idx.Upsert(id, b)
			model[id] = b
		}
		if op%97 == 0 {
			genBefore = idx.Generation()
			idx.Rebuild()
			if idx.Generation() != genBefore+1 {
				t.Fatalf("generation did not advance: %d -> %d", genBefore, idx.Generation())
			}
		}
		checkQuery(t, idx, model, randBox())
		if op%13 == 0 {
			checkQuery(t, idx, model, Box{MinX: -1e9, MinY: -1e9, MaxX: 1e9, MaxY: 1e9}) // cover everything
		}
	}
	idx.Rebuild()
	if idx.DeltaLen() != 0 {
		t.Fatalf("delta not drained after quiescent rebuild, len=%d", idx.DeltaLen())
	}
	checkQuery(t, idx, model, Box{MinX: -1e9, MinY: -1e9, MaxX: 1e9, MaxY: 1e9})
}

// TestDeleteRecreateSameID covers deleting an id and re-inserting it
// with a new box, in every rebuild interleaving.
func TestDeleteRecreateSameID(t *testing.T) {
	boxA := Box{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1}
	boxB := Box{MinX: 100, MinY: 100, MaxX: 101, MaxY: 101}
	regionA := Box{MinX: -1, MinY: -1, MaxX: 2, MaxY: 2}
	regionB := Box{MinX: 99, MinY: 99, MaxX: 102, MaxY: 102}

	// rebuildMask bit 0: rebuild between delete and recreate;
	// bit 1: rebuild after recreate.
	for mask := 0; mask < 4; mask++ {
		idx := New()
		idx.Upsert(7, boxA)
		idx.Rebuild()

		idx.Delete(7)
		if got := idx.Query(regionA); len(got) != 0 {
			t.Fatalf("mask=%d: tombstone failed, got %v", mask, got)
		}
		if mask&1 != 0 {
			idx.Rebuild()
		}
		idx.Upsert(7, boxB)
		if mask&2 != 0 {
			idx.Rebuild()
		}
		if got := idx.Query(regionA); len(got) != 0 {
			t.Fatalf("mask=%d: stale box A still visible: %v", mask, got)
		}
		got := idx.Query(regionB)
		if !reflect.DeepEqual(got, []uint64{7}) {
			t.Fatalf("mask=%d: recreated object missing, got %v", mask, got)
		}
		// Full rebuild must not resurrect the old version.
		idx.Rebuild()
		if got := idx.Query(regionA); len(got) != 0 {
			t.Fatalf("mask=%d: rebuild resurrected box A: %v", mask, got)
		}
		if got := idx.Query(regionB); !reflect.DeepEqual(got, []uint64{7}) {
			t.Fatalf("mask=%d: rebuild lost recreated object: %v", mask, got)
		}
	}
}

// TestEmptyBoxes verifies inverted boxes are never matched and empty
// queries match nothing, in lockstep with the full-scan reference.
func TestEmptyBoxes(t *testing.T) {
	idx := New()
	model := make(map[uint64]Box)

	empty := Box{MinX: 5, MinY: 5, MaxX: 0, MaxY: 0}
	point := Box{MinX: 3, MinY: 3, MaxX: 3, MaxY: 3} // zero-area but valid
	normal := Box{MinX: 0, MinY: 0, MaxX: 2, MaxY: 2}

	idx.Upsert(1, empty)
	idx.Upsert(2, point)
	idx.Upsert(3, normal)
	model[1], model[2], model[3] = empty, point, normal

	universe := Box{MinX: -1e9, MinY: -1e9, MaxX: 1e9, MaxY: 1e9}
	checkQuery(t, idx, model, universe) // id 1 must be absent, 2 and 3 present

	if got := idx.Query(empty); got != nil {
		t.Fatalf("empty query box returned %v", got)
	}
	checkQuery(t, idx, model, Box{MinX: 3, MinY: 3, MaxX: 3, MaxY: 3}) // point query hits id 2

	// Move id 3 onto an empty box: it must vanish from results.
	idx.Upsert(3, empty)
	model[3] = empty
	idx.Rebuild()
	checkQuery(t, idx, model, universe)
	if got := idx.Query(universe); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("expected only id 2, got %v", got)
	}
}

// TestRebuildKeepsMovesDuringRebuild drives snapshot/commit directly to
// deterministically interleave mutations with an in-flight rebuild.
func TestRebuildKeepsMovesDuringRebuild(t *testing.T) {
	idx := New()
	for id := uint64(1); id <= 10; id++ {
		idx.Upsert(id, Box{MinX: float64(id), MinY: 0, MaxX: float64(id) + 0.5, MaxY: 1})
	}
	idx.Rebuild() // fold into stable tree
	gen := idx.Generation()

	// Start a rebuild: snapshot taken, tree built "in the background".
	items, snapV := idx.snapshot()
	root := buildTree(items)

	// Moves that land while the rebuild is in flight.
	idx.Upsert(1, Box{MinX: 1000, MinY: 1000, MaxX: 1001, MaxY: 1001})  // move existing
	idx.Delete(2)                                                       // delete existing
	idx.Upsert(11, Box{MinX: 2000, MinY: 2000, MaxX: 2001, MaxY: 2001}) // brand new
	idx.Upsert(3, Box{MinX: 3, MinY: 0, MaxX: 3.5, MaxY: 1})            // no-op-ish move

	idx.commit(root, snapV)

	if idx.Generation() != gen+1 {
		t.Fatalf("generation not bumped: %d -> %d", gen, idx.Generation())
	}
	// In-flight mutations must survive the swap...
	if idx.DeltaLen() != 4 {
		t.Fatalf("in-flight deltas lost, delta len = %d, want 4", idx.DeltaLen())
	}
	// ...and pre-snapshot deltas must be drained.
	model := map[uint64]Box{}
	for id := uint64(1); id <= 10; id++ {
		model[id] = Box{MinX: float64(id), MinY: 0, MaxX: float64(id) + 0.5, MaxY: 1}
	}
	model[1] = Box{MinX: 1000, MinY: 1000, MaxX: 1001, MaxY: 1001}
	delete(model, 2)
	model[11] = Box{MinX: 2000, MinY: 2000, MaxX: 2001, MaxY: 2001}

	universe := Box{MinX: -1e9, MinY: -1e9, MaxX: 1e9, MaxY: 1e9}
	checkQuery(t, idx, model, universe)
	checkQuery(t, idx, model, Box{MinX: 999, MinY: 999, MaxX: 1002, MaxY: 1002})
	checkQuery(t, idx, model, Box{MinX: 1.5, MinY: 0, MaxX: 2.6, MaxY: 1}) // id 2's old spot

	// A quiescent rebuild now drains everything.
	idx.Rebuild()
	if idx.DeltaLen() != 0 {
		t.Fatalf("delta not drained, len=%d", idx.DeltaLen())
	}
	checkQuery(t, idx, model, universe)
}

// TestConcurrentRebuildAndMoves runs writers and a rebuild loop
// concurrently; the final state must match the model exactly.
func TestConcurrentRebuildAndMoves(t *testing.T) {
	idx := New()
	var mu sync.Mutex
	model := make(map[uint64]Box)

	var writers sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(seed int64) {
			defer writers.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 2000; i++ {
				id := uint64(rng.Intn(500))
				if rng.Intn(4) == 0 {
					idx.Delete(id)
					mu.Lock()
					delete(model, id)
					mu.Unlock()
				} else {
					x := rng.Float64() * 1000
					b := Box{MinX: x, MinY: x, MaxX: x + 1, MaxY: x + 1}
					idx.Upsert(id, b)
					mu.Lock()
					model[id] = b
					mu.Unlock()
				}
			}
		}(int64(w) + 1)
	}

	rebuilderDone := make(chan struct{})
	go func() {
		defer close(rebuilderDone)
		for {
			select {
			case <-stop:
				return
			default:
				idx.Rebuild()
			}
		}
	}()

	writers.Wait()
	close(stop)
	<-rebuilderDone

	// Quiesce: one final rebuild, then every query must match a full
	// scan of the model.
	idx.Rebuild()
	if idx.DeltaLen() != 0 {
		t.Fatalf("delta not drained after quiescent rebuild, len=%d", idx.DeltaLen())
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		x := rng.Float64() * 1000
		checkQuery(t, idx, model, Box{MinX: x, MinY: x, MaxX: x + 50, MaxY: x + 50})
	}
	checkQuery(t, idx, model, Box{MinX: -1e9, MinY: -1e9, MaxX: 1e9, MaxY: 1e9})
}
