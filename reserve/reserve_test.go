package reserve

import (
	"math/rand"
	"sync"
	"testing"
)

func mustBook(t *testing.T, s *Store, v uint64, r Reservation) uint64 {
	t.Helper()
	nv, err := s.Book(v, r)
	if err != nil {
		t.Fatalf("Book(%+v) failed: %v", r, err)
	}
	return nv
}

func TestBookAndVersion(t *testing.T) {
	s := NewStore(10)
	v := mustBook(t, s, 0, Reservation{ID: "a", Start: 0, End: 10, Capacity: 6})
	if v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	// Overlapping, would exceed capacity: 6 + 5 > 10.
	if _, err := s.Book(v, Reservation{ID: "b", Start: 5, End: 15, Capacity: 5}); err != ErrConflict {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	// Failed book must not bump the version.
	if got := s.Version(); got != 1 {
		t.Fatalf("version after failed book = %d, want 1", got)
	}
	// Adjacent (half-open) intervals do not overlap: [10,20) fits next to [0,10).
	v = mustBook(t, s, 1, Reservation{ID: "b", Start: 10, End: 20, Capacity: 10})
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
}

func TestStaleVersionRejected(t *testing.T) {
	s := NewStore(10)
	mustBook(t, s, 0, Reservation{ID: "a", Start: 0, End: 10, Capacity: 1})
	if _, err := s.Book(0, Reservation{ID: "b", Start: 20, End: 30, Capacity: 1}); err != ErrVersionMismatch {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
	if _, err := s.Modify(0, "a", 0, 5, 1); err != ErrVersionMismatch {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
	if _, err := s.Cancel(0, "a"); err != ErrVersionMismatch {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
	if s.Version() != 1 {
		t.Fatalf("version = %d, want 1", s.Version())
	}
}

// TestModifyAtomicOnConflict is the core guarantee: a conflicting modify must
// leave the old reservation in place, not release it and lose it.
func TestModifyAtomicOnConflict(t *testing.T) {
	s := NewStore(10)
	v := mustBook(t, s, 0, Reservation{ID: "a", Start: 0, End: 10, Capacity: 6})
	v = mustBook(t, s, v, Reservation{ID: "b", Start: 10, End: 20, Capacity: 6})

	// Moving "a" into b's interval conflicts; old interval must survive.
	if _, err := s.Modify(v, "a", 5, 15, 6); err != ErrConflict {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	got, ok := s.Get("a")
	if !ok {
		t.Fatal("reservation a lost after failed modify")
	}
	if got.Start != 0 || got.End != 10 || got.Capacity != 6 {
		t.Fatalf("a mutated to %+v after failed modify", got)
	}
	if s.Version() != v {
		t.Fatalf("version bumped by failed modify: %d != %d", s.Version(), v)
	}

	// A modify that fits must atomically swap: shrink+move succeeds.
	nv, err := s.Modify(v, "a", 0, 4, 6)
	if err != nil {
		t.Fatalf("valid modify failed: %v", err)
	}
	got, _ = s.Get("a")
	if got.Start != 0 || got.End != 4 {
		t.Fatalf("a = %+v after modify", got)
	}
	if nv != v+1 || s.Version() != v+1 {
		t.Fatalf("version = %d, want %d", s.Version(), v+1)
	}

	// Growing into one's own interval extension is fine (self excluded).
	if _, err := s.Modify(s.Version(), "a", 0, 10, 10); err != nil {
		t.Fatalf("self-extension modify failed: %v", err)
	}
}

func TestZeroLengthReservation(t *testing.T) {
	s := NewStore(1)
	v := mustBook(t, s, 0, Reservation{ID: "full", Start: 0, End: 100, Capacity: 1})
	// Zero-length reservation with huge capacity occupies nothing.
	v = mustBook(t, s, v, Reservation{ID: "z", Start: 50, End: 50, Capacity: 1 << 40})
	if got := s.MaxLoad(); got != 1 {
		t.Fatalf("MaxLoad = %d, want 1", got)
	}
	// Zero-length modify onto a full interval also fits.
	if _, err := s.Modify(v, "z", 0, 0, 1<<40); err != nil {
		t.Fatalf("zero-length modify failed: %v", err)
	}
	// start > end is invalid and changes nothing.
	if _, err := s.Book(s.Version(), Reservation{ID: "bad", Start: 5, End: 4, Capacity: 1}); err != ErrInvalidInterval {
		t.Fatalf("err = %v, want ErrInvalidInterval", err)
	}
	if _, err := s.Modify(s.Version(), "z", 9, 3, 1); err != ErrInvalidInterval {
		t.Fatalf("err = %v, want ErrInvalidInterval", err)
	}
	if _, err := s.Book(s.Version(), Reservation{ID: "neg", Start: 0, End: 1, Capacity: -1}); err != ErrInvalidCapacity {
		t.Fatalf("err = %v, want ErrInvalidCapacity", err)
	}
}

func TestCancelRetryIdempotent(t *testing.T) {
	s := NewStore(10)
	v := mustBook(t, s, 0, Reservation{ID: "a", Start: 0, End: 10, Capacity: 5})
	v2, err := s.Cancel(v, "a")
	if err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if v2 != v+1 {
		t.Fatalf("version = %d, want %d", v2, v+1)
	}
	// Retry with the new version: success, no state change, no version bump.
	v3, err := s.Cancel(v2, "a")
	if err != nil {
		t.Fatalf("cancel retry failed: %v", err)
	}
	if v3 != v2 || s.Version() != v2 {
		t.Fatalf("retry changed version: %d -> %d", v2, s.Version())
	}
	// Capacity is reusable after cancel.
	mustBook(t, s, v2, Reservation{ID: "b", Start: 0, End: 10, Capacity: 10})
}

// TestConcurrentModify hammers the store with conflicting and non-conflicting
// modifies from many goroutines, then audits the final state with an
// independent endpoint scan: capacity never exceeded, and every reservation
// is in one of its legal placements (never lost, never half-swapped).
func TestConcurrentModify(t *testing.T) {
	const capacity = 10
	s := NewStore(capacity)
	v := mustBook(t, s, 0, Reservation{ID: "a", Start: 0, End: 10, Capacity: 6})
	v = mustBook(t, s, v, Reservation{ID: "b", Start: 10, End: 20, Capacity: 6})

	// Legal targets for each ID: index 0 is the original placement.
	targets := map[string][]Reservation{
		"a": {
			{ID: "a", Start: 0, End: 10, Capacity: 6},
			{ID: "a", Start: 0, End: 4, Capacity: 6},  // shrink: fits
			{ID: "a", Start: 5, End: 15, Capacity: 6}, // overlaps b: conflict
		},
		"b": {
			{ID: "b", Start: 10, End: 20, Capacity: 6},
			{ID: "b", Start: 16, End: 20, Capacity: 6}, // shrink: fits
			{ID: "b", Start: 5, End: 15, Capacity: 6},  // overlaps a: conflict
		},
	}

	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				id := []string{"a", "b"}[rng.Intn(2)]
				tgt := targets[id][rng.Intn(3)]
				// Concurrent modifies race on version; retry on mismatch.
				for {
					ver := s.Version()
					_, err := s.Modify(ver, id, tgt.Start, tgt.End, tgt.Capacity)
					if err == ErrVersionMismatch {
						continue
					}
					if err != nil && err != ErrConflict {
						t.Errorf("unexpected error: %v", err)
					}
					break
				}
			}
		}(g)
	}
	wg.Wait()

	// Audit: capacity invariant holds under the endpoint-scan model...
	if load := s.MaxLoad(); load > capacity {
		t.Fatalf("MaxLoad = %d exceeds capacity %d", load, capacity)
	}
	// ...and both reservations still exist in a legal placement.
	for id, opts := range targets {
		got, ok := s.Get(id)
		if !ok {
			t.Fatalf("reservation %s lost under concurrent modify", id)
		}
		legal := false
		for _, o := range opts {
			if got == o {
				legal = true
			}
		}
		if !legal {
			t.Fatalf("reservation %s in impossible state %+v", id, got)
		}
	}
}

// TestConcurrentBookCancel races books and cancels on disjoint IDs and
// verifies the version equals the number of successful mutations and the
// final load never exceeds capacity.
func TestConcurrentBookCancel(t *testing.T) {
	const capacity = 4
	s := NewStore(capacity)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes int

	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 100))
			id := string(rune('a'+g)) + "-x"
			for i := 0; i < 100; i++ {
				ver := s.Version()
				r := Reservation{
					ID:       id,
					Start:    int64(rng.Intn(50)),
					Capacity: int64(1 + rng.Intn(3)),
				}
				r.End = r.Start + int64(1+rng.Intn(10))
				if _, err := s.Book(ver, r); err == nil {
					mu.Lock()
					successes++
					mu.Unlock()
					for {
						ver = s.Version()
						_, err := s.Cancel(ver, id)
						if err == ErrVersionMismatch {
							continue
						}
						if err == nil {
							mu.Lock()
							successes++
							mu.Unlock()
						}
						break
					}
				}
			}
		}(g)
	}
	wg.Wait()

	if got := s.Version(); got != uint64(successes) {
		t.Fatalf("version = %d, want %d (one per successful mutation)", got, successes)
	}
	if load := s.MaxLoad(); load > capacity {
		t.Fatalf("MaxLoad = %d exceeds capacity %d", load, capacity)
	}
	if n := len(s.res); n != 0 {
		t.Fatalf("%d reservations leaked", n)
	}
}

// TestEndpointScanModel cross-checks maxLoad against a brute-force
// point-by-point model on random instances.
func TestEndpointScanModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 500; trial++ {
		n := rng.Intn(12)
		res := make([]Reservation, 0, n)
		for i := 0; i < n; i++ {
			start := int64(rng.Intn(20))
			r := Reservation{
				ID:       string(rune('a' + i)),
				Start:    start,
				End:      start + int64(rng.Intn(8)), // includes zero-length
				Capacity: int64(rng.Intn(5)),
			}
			res = append(res, r)
		}
		got := maxLoad(res, "")
		// Brute force: sample every integer instant in [0, 28).
		var want int64
		for ts := int64(0); ts < 28; ts++ {
			var load int64
			for _, r := range res {
				if r.Start <= ts && ts < r.End {
					load += r.Capacity
				}
			}
			if load > want {
				want = load
			}
		}
		if got != want {
			t.Fatalf("trial %d: maxLoad = %d, brute force = %d, res = %+v", trial, got, want, res)
		}
	}
}
