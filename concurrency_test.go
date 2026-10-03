package hexgrid

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestStoreConcurrentAccess hammers the store, the K-ring traversal and
// the inspector from many goroutines. Run with -race.
func TestStoreConcurrentAccess(t *testing.T) {
	center := beijingCell(t)
	disk, err := KRing(center, 3)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	ins := DefaultInspector()
	// Disjoint key space per worker: each worker owns the cells whose
	// last digit equals its id, so Get-after-Upsert is race-free.
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			var mine []Cell
			for idx, c := range disk {
				if idx%8 == w {
					mine = append(mine, c)
				}
			}
			for i := 0; i < 300; i++ {
				c := mine[i%len(mine)]
				s.Upsert(Record{Cell: c, Payload: int64(i)})
				if _, ok := s.Get(c); !ok {
					t.Errorf("record %s lost", c)
					return
				}
				if i%3 == 0 {
					s.Delete(c)
				}
				if i%7 == 0 {
					_ = s.Snapshot()
				}
				if i%11 == 0 {
					_ = s.Len()
				}
				if i%13 == 0 {
					_ = ins.Inspect(s)
				}
				if _, err := KRing(c, 2); err != nil {
					t.Errorf("k-ring: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

// TestKRingConcurrentPure verifies the traversal is a pure function
// safe for concurrent use without any synchronization.
func TestKRingConcurrentPure(t *testing.T) {
	center := beijingCell(t)
	want, err := KRing(center, 3)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				got, err := KRing(center, 3)
				if err != nil {
					t.Error(err)
					return
				}
				if len(got) != len(want) {
					t.Errorf("ring size %d, want %d", len(got), len(want))
					return
				}
				for j := range got {
					if got[j] != want[j] {
						t.Errorf("ring cell %d differs under concurrency", j)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// TestNoGoroutineLeak asserts the library spawns no goroutines and
// leaves none behind: strictly no leaks.
func TestNoGoroutineLeak(t *testing.T) {
	settle := func() int {
		prev := -1
		for i := 0; i < 200; i++ {
			n := runtime.NumGoroutine()
			if n == prev {
				return n
			}
			prev = n
			time.Sleep(5 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			s := NewStore()
			ll := FaceCenter(w % NumBaseCells)
			center, err := LatLngToCell(ll.Lat, ll.Lng, 8)
			if err != nil {
				t.Error(err)
				return
			}
			disk, err := KRing(center, 2)
			if err != nil {
				t.Error(err)
				return
			}
			for _, c := range disk {
				s.Upsert(Record{Cell: c})
			}
			_ = DefaultInspector().Inspect(s)
		}(w)
	}
	wg.Wait()
	if after := settle(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestStoreNoAliasing verifies records are deep-copied in and out, so
// callers can never mutate (leak into) internal store state.
func TestStoreNoAliasing(t *testing.T) {
	s := NewStore()
	c := MustCell(3, 8, []uint8{1, 2, 3, 4, 5, 6, 0, 1})
	nb := MustCell(3, 8, []uint8{1, 2, 3, 4, 5, 6, 0, 2})
	nbs := []Cell{nb}
	s.Upsert(Record{Cell: c, Neighbors: nbs})
	nbs[0] = Cell(0) // mutate caller slice after upsert
	r, ok := s.Get(c)
	if !ok {
		t.Fatal("record missing")
	}
	if r.Neighbors[0] != nb {
		t.Fatal("store aliased the caller's neighbor slice")
	}
	r.Neighbors[0] = Cell(0) // mutate the returned record
	r2, _ := s.Get(c)
	if r2.Neighbors[0] != nb {
		t.Fatal("Get returned aliased internal state")
	}
	snap := s.Snapshot()
	snap[0].Neighbors[0] = Cell(0)
	r3, _ := s.Get(c)
	if r3.Neighbors[0] != nb {
		t.Fatal("Snapshot returned aliased internal state")
	}
}
