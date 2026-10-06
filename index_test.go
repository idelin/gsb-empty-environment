package bboxidx

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// randomBox returns a normal box, a point box, or an empty box.
func randomBox(rng *rand.Rand) Box {
	switch rng.Intn(10) {
	case 0: // empty box: Min > Max
		x := rng.Float64() * 1000
		y := rng.Float64() * 1000
		return Box{MinX: x + 1, MinY: y, MaxX: x, MaxY: y + 5}
	case 1: // point box: zero area
		x := rng.Float64() * 1000
		y := rng.Float64() * 1000
		return Box{MinX: x, MinY: y, MaxX: x, MaxY: y}
	default:
		x := rng.Float64() * 1000
		y := rng.Float64() * 1000
		w := rng.Float64() * 50
		h := rng.Float64() * 50
		return Box{MinX: x, MinY: y, MaxX: x + w, MaxY: y + h}
	}
}

// scan is the brute-force reference: a full scan over all live objects.
func scan(model map[uint64]Box, q Box) []uint64 {
	var ids []uint64
	for id, b := range model {
		if b.Intersects(q) {
			ids = append(ids, id)
		}
	}
	sortUint64(ids)
	return ids
}

func sortUint64(s []uint64) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

var wholeSpace = Box{MinX: -1e18, MinY: -1e18, MaxX: 1e18, MaxY: 1e18}

func checkQuery(t *testing.T, idx *Index, model map[uint64]Box, q Box) {
	t.Helper()
	got := idx.Query(q)
	want := scan(model, q)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query %+v: got %v, want %v", q, got, want)
	}
}

// TestModelAgainstFullScan hammers the index with random upserts, deletes,
// delete-then-recreate cycles and empty boxes while a background goroutine
// rebuilds continuously, checking random queries against a full scan.
func TestModelAgainstFullScan(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	idx := New()
	model := make(map[uint64]Box)
	var graveyard []uint64 // deleted ids eligible for re-creation

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				idx.Rebuild()
			}
		}
	}()

	for i := 0; i < 20000; i++ {
		switch op := rng.Intn(100); {
		case op < 50: // upsert new or existing id
			id := uint64(rng.Intn(500))
			b := randomBox(rng)
			idx.Upsert(id, b)
			model[id] = b
		case op < 70: // delete a live id
			if len(model) == 0 {
				continue
			}
			id := uint64(rng.Intn(500))
			if _, ok := model[id]; !ok {
				continue
			}
			idx.Delete(id)
			delete(model, id)
			graveyard = append(graveyard, id)
		case op < 80: // re-create a previously deleted id
			if len(graveyard) == 0 {
				continue
			}
			id := graveyard[rng.Intn(len(graveyard))]
			b := randomBox(rng)
			idx.Upsert(id, b)
			model[id] = b
		default: // verify a random query against the full scan
			checkQuery(t, idx, model, randomBox(rng))
		}
	}

	close(stop)
	wg.Wait()

	// A final quiescent rebuild must leave an empty delta and a stable
	// tree that alone answers every query correctly.
	idx.Rebuild()
	if got := idx.DeltaLen(); got != 0 {
		t.Fatalf("delta not drained after quiescent rebuild: %d entries", got)
	}
	if idx.Generation() == 0 {
		t.Fatal("expected at least one rebuild generation")
	}
	for i := 0; i < 200; i++ {
		checkQuery(t, idx, model, randomBox(rng))
	}
	checkQuery(t, idx, model, wholeSpace)
}

// TestDeleteThenRecreateSameID checks that a re-created id shadows its
// deleted state across rebuild boundaries.
func TestDeleteThenRecreateSameID(t *testing.T) {
	idx := New()
	boxA := Box{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}
	boxB := Box{MinX: 100, MinY: 100, MaxX: 110, MaxY: 110}

	idx.Upsert(1, boxA)
	idx.Rebuild()
	idx.Delete(1)
	idx.Rebuild()
	if got := idx.Query(boxA); len(got) != 0 {
		t.Fatalf("deleted id still visible: %v", got)
	}

	idx.Upsert(1, boxB) // same id, new box, newer version
	if got := idx.Query(boxB); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("re-created id not found at new box: %v", got)
	}
	if got := idx.Query(boxA); len(got) != 0 {
		t.Fatalf("re-created id still visible at old box: %v", got)
	}

	idx.Rebuild() // tombstone and stale state must be gone for good
	if got := idx.Query(boxB); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("re-created id lost after rebuild: %v", got)
	}
	if got := idx.Query(boxA); len(got) != 0 {
		t.Fatalf("stale box resurrected after rebuild: %v", got)
	}

	idx.Delete(1)
	idx.Rebuild()
	if got := idx.Query(wholeSpace); len(got) != 0 {
		t.Fatalf("tombstone did not survive rebuild: %v", got)
	}
}

// TestEmptyBoxes pins down empty-box semantics: objects with empty boxes
// never match, and empty query boxes match nothing.
func TestEmptyBoxes(t *testing.T) {
	idx := New()
	empty := Box{MinX: 6, MinY: 6, MaxX: 5, MaxY: 5}
	point := Box{MinX: 5, MinY: 5, MaxX: 5, MaxY: 5}
	normal := Box{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}

	idx.Upsert(1, empty)
	idx.Upsert(2, point)
	idx.Upsert(3, normal)

	if got := idx.Query(wholeSpace); !reflect.DeepEqual(got, []uint64{2, 3}) {
		t.Fatalf("empty-box object matched whole-space query: %v", got)
	}
	if got := idx.Query(empty); len(got) != 0 {
		t.Fatalf("empty query box matched: %v", got)
	}
	if got := idx.Query(point); !reflect.DeepEqual(got, []uint64{2, 3}) {
		t.Fatalf("point query: got %v, want [2 3]", got)
	}

	// Moving an object onto an empty box hides it, across a rebuild.
	idx.Upsert(3, empty)
	idx.Rebuild()
	if got := idx.Query(wholeSpace); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("object with empty box still visible: %v", got)
	}
}

// TestRebuildPreservesConcurrentMoves moves objects while a rebuild is in
// flight and verifies no move is lost by the snapshot swap.
func TestRebuildPreservesConcurrentMoves(t *testing.T) {
	idx := New()
	const n = 200
	for id := uint64(0); id < n; id++ {
		idx.Upsert(id, Box{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1})
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				idx.Rebuild()
			}
		}
	}()

	// Every object marches to its own far-away box, one move at a time.
	model := make(map[uint64]Box, n)
	for round := 0; round < 20; round++ {
		for id := uint64(0); id < n; id++ {
			x := float64(1000+round*10) + float64(id)
			b := Box{MinX: x, MinY: x, MaxX: x + 1, MaxY: x + 1}
			idx.Upsert(id, b)
			model[id] = b
		}
	}
	close(done)

	idx.Rebuild()
	for id, b := range model {
		want := scan(model, b)
		if got := idx.Query(b); !reflect.DeepEqual(got, want) {
			t.Fatalf("move lost for id %d: got %v, want %v", id, got, want)
		}
	}
	if got := idx.Query(Box{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1}); len(got) != 0 {
		t.Fatalf("stale positions survived rebuild: %v", got)
	}
	if got := idx.DeltaLen(); got != 0 {
		t.Fatalf("delta not drained: %d entries", got)
	}
}

// TestConcurrentReadersAndWriters exercises the locking under -race with
// queries, mutations and rebuilds all in flight.
func TestConcurrentReadersAndWriters(t *testing.T) {
	idx := New()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 2000; i++ {
				id := uint64(rng.Intn(100))
				switch rng.Intn(3) {
				case 0:
					idx.Upsert(id, randomBox(rng))
				case 1:
					idx.Delete(id)
				default:
					idx.Query(randomBox(rng))
				}
			}
		}(int64(w))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			idx.Rebuild()
		}
	}()
	wg.Wait()
}

// TestConcurrentRebuildsNeverLoseMoves runs several rebuild goroutines at
// once so snapshot swaps can complete out of order; no move may be lost.
func TestConcurrentRebuildsNeverLoseMoves(t *testing.T) {
	idx := New()
	const n = 100
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					idx.Rebuild()
				}
			}
		}()
	}

	model := make(map[uint64]Box, n)
	for round := 0; round < 30; round++ {
		for id := uint64(0); id < n; id++ {
			x := float64(round*n) + float64(id)*2
			b := Box{MinX: x, MinY: 0, MaxX: x + 1, MaxY: 1}
			idx.Upsert(id, b)
			model[id] = b
		}
	}
	close(stop)
	wg.Wait()
	idx.Rebuild()

	for i := 0; i < 100; i++ {
		checkQuery(t, idx, model, randomBox(rand.New(rand.NewSource(int64(i)))))
	}
	checkQuery(t, idx, model, wholeSpace)
	if got := idx.DeltaLen(); got != 0 {
		t.Fatalf("delta not drained: %d entries", got)
	}
}
