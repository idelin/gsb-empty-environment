package occupancy

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// --- Endpoint-scan reference model -------------------------------------
//
// The model is intentionally independent of the Store implementation: it
// collects every interval endpoint, walks the elementary segments between
// consecutive endpoints, and sums the capacity of every reservation whose
// half-open interval covers that segment.

// modelUsageOK reports whether the given set of reservations never exceeds
// total capacity on any elementary segment.
func modelUsageOK(res []Reservation, total int64) bool {
	var points []int64
	for _, r := range res {
		if r.Start == r.End || r.Capacity == 0 {
			continue
		}
		points = append(points, r.Start, r.End)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	uniq := points[:0]
	for i, p := range points {
		if i == 0 || p != points[i-1] {
			uniq = append(uniq, p)
		}
	}
	for i := 0; i+1 < len(uniq); i++ {
		lo, hi := uniq[i], uniq[i+1]
		var used int64
		for _, r := range res {
			if r.Start <= lo && r.End >= hi { // covers the whole segment
				used += r.Capacity
			}
		}
		if used > total {
			return false
		}
	}
	return true
}

// modelFits predicts whether placing cap units over [start, end) is legal
// given the existing reservations (excluding excludeID).
func modelFits(res []Reservation, excludeID string, total, start, end, cap int64) bool {
	if start == end || cap == 0 {
		return true
	}
	cand := make([]Reservation, 0, len(res)+1)
	for _, r := range res {
		if r.ID != excludeID {
			cand = append(cand, r)
		}
	}
	cand = append(cand, Reservation{ID: "__candidate__", Start: start, End: end, Capacity: cap})
	return modelUsageOK(cand, total)
}

func checkState(t *testing.T, s *Store, total int64) {
	t.Helper()
	if !modelUsageOK(s.Snapshot(), total) {
		t.Fatalf("endpoint-scan model: capacity exceeded in store state: %v", s.Snapshot())
	}
}

// --- Targeted tests ------------------------------------------------------

func TestModifyAtomicOnConflict(t *testing.T) {
	s := NewStore(10)
	if err := s.Reserve(0, Reservation{ID: "a", Start: 0, End: 10, Capacity: 6}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve(1, Reservation{ID: "b", Start: 0, End: 10, Capacity: 4}); err != nil {
		t.Fatal(err)
	}
	// Moving "b" is fine, but growing "b" to 7 would exceed capacity
	// (6+7 > 10). The modify must fail and "b" must keep its old placement.
	err := s.Modify(2, "b", 2, 8, 7)
	if err != ErrInsufficientCapacity {
		t.Fatalf("want ErrInsufficientCapacity, got %v", err)
	}
	got, ok := s.Get("b")
	if !ok {
		t.Fatal("old reservation lost after failed modify")
	}
	want := Reservation{ID: "b", Start: 0, End: 10, Capacity: 4}
	if got != want {
		t.Fatalf("old reservation mutated: got %+v want %+v", got, want)
	}
	if v := s.Version(); v != 2 {
		t.Fatalf("version changed on failed modify: %d", v)
	}
	// A modify that only overlaps itself must succeed: growing "a" into
	// capacity freed by cancelling "b" is atomic as well.
	if err := s.Cancel(2, "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Modify(3, "a", 0, 20, 10); err != nil {
		t.Fatalf("modify after cancel: %v", err)
	}
	checkState(t, s, 10)
}

func TestVersionConflictLeavesStateUnchanged(t *testing.T) {
	s := NewStore(5)
	if err := s.Reserve(0, Reservation{ID: "a", Start: 0, End: 5, Capacity: 2}); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	stale := uint64(0)
	if err := s.Reserve(stale, Reservation{ID: "b", Start: 0, End: 5, Capacity: 1}); err != ErrVersionConflict {
		t.Fatalf("reserve: %v", err)
	}
	if err := s.Modify(stale, "a", 1, 4, 1); err != ErrVersionConflict {
		t.Fatalf("modify: %v", err)
	}
	if err := s.Cancel(stale, "a"); err != ErrVersionConflict {
		t.Fatalf("cancel: %v", err)
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("state changed on version conflict")
	}
	if v := s.Version(); v != 1 {
		t.Fatalf("version changed on version conflict: %d", v)
	}
}

func TestZeroLengthReservations(t *testing.T) {
	s := NewStore(3)
	// Zero-length reservations at the same instant with any capacity never
	// conflict, even beyond total capacity: they occupy no time.
	for i := 0; i < 50; i++ {
		err := s.Reserve(s.Version(), Reservation{
			ID: fmt.Sprintf("z%d", i), Start: 7, End: 7, Capacity: 1000,
		})
		if err != nil {
			t.Fatalf("zero-length reserve %d: %v", i, err)
		}
	}
	// A real reservation still sees full capacity available.
	if err := s.Reserve(s.Version(), Reservation{ID: "real", Start: 0, End: 100, Capacity: 3}); err != nil {
		t.Fatalf("real reserve alongside zero-length: %v", err)
	}
	// Modifying a zero-length reservation into a real one must obey capacity.
	if err := s.Modify(s.Version(), "z0", 0, 100, 1); err != ErrInsufficientCapacity {
		t.Fatalf("want ErrInsufficientCapacity, got %v", err)
	}
	// Modifying a real reservation down to zero length frees its capacity.
	if err := s.Modify(s.Version(), "real", 42, 42, 3); err != nil {
		t.Fatalf("shrink to zero length: %v", err)
	}
	if err := s.Reserve(s.Version(), Reservation{ID: "real2", Start: 0, End: 100, Capacity: 3}); err != nil {
		t.Fatalf("capacity not freed by zero-length modify: %v", err)
	}
	checkState(t, s, 3)
}

func TestCancelRetry(t *testing.T) {
	s := NewStore(5)
	if err := s.Reserve(0, Reservation{ID: "a", Start: 0, End: 5, Capacity: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(1, "a"); err != nil {
		t.Fatal(err)
	}
	v := s.Version()
	before := s.Snapshot()
	// Retry with the pre-cancel version: version conflict, not success.
	if err := s.Cancel(1, "a"); err != ErrVersionConflict {
		t.Fatalf("stale retry: want ErrVersionConflict, got %v", err)
	}
	// Retry with the current version: not found, state untouched.
	if err := s.Cancel(v, "a"); err != ErrNotFound {
		t.Fatalf("fresh retry: want ErrNotFound, got %v", err)
	}
	if err := s.Cancel(s.Version(), "never-existed"); err != ErrNotFound {
		t.Fatalf("unknown id: want ErrNotFound, got %v", err)
	}
	if s.Version() != v {
		t.Fatal("version changed by failed cancel retries")
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("state changed by failed cancel retries")
	}
}

// --- Concurrency test ----------------------------------------------------

// Hammer the store from many goroutines with read-version-then-act retry
// loops, then verify the final state with the endpoint-scan model and check
// that the version advanced exactly once per successful mutation.
func TestConcurrentModify(t *testing.T) {
	const total = 16
	s := NewStore(total)
	var successes int64

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 1))
			id := fmt.Sprintf("g%d", g)
			for i := 0; i < 200; i++ {
				start := rng.Int63n(50)
				end := start + rng.Int63n(10) // may be zero-length
				cap := 1 + rng.Int63n(8)
				v := s.Version()
				var err error
				switch rng.Intn(3) {
				case 0:
					err = s.Reserve(v, Reservation{ID: id, Start: start, End: end, Capacity: cap})
					if err == ErrDuplicateID {
						err = s.Modify(v, id, start, end, cap)
					}
				case 1:
					err = s.Modify(v, id, start, end, cap)
				case 2:
					err = s.Cancel(v, id)
				}
				if err == nil {
					atomic.AddInt64(&successes, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	if got, want := s.Version(), uint64(successes); got != want {
		t.Fatalf("version = %d, want exactly %d successful mutations", got, want)
	}
	checkState(t, s, total)
}

// --- Randomized model cross-check ---------------------------------------

// Drive random operations through the Store and a plain map model in
// lockstep. The model predicts success/failure via the endpoint scan; the
// store must agree on every outcome and on the resulting state.
func TestRandomOpsAgainstModel(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		const total = 8
		s := NewStore(total)
		model := make(map[string]Reservation)
		version := uint64(0)

		for step := 0; step < 500; step++ {
			id := fmt.Sprintf("r%d", rng.Intn(12))
			start := rng.Int63n(30)
			end := start + rng.Int63n(6) // zero-length possible
			cap := rng.Int63n(6)         // zero capacity possible

			snap := make([]Reservation, 0, len(model))
			for _, r := range model {
				snap = append(snap, r)
			}

			var err error
			var expectErr error
			switch rng.Intn(3) {
			case 0: // reserve
				r := Reservation{ID: id, Start: start, End: end, Capacity: cap}
				if _, dup := model[id]; dup {
					expectErr = ErrDuplicateID
				} else if !modelFits(snap, "", total, start, end, cap) {
					expectErr = ErrInsufficientCapacity
				}
				err = s.Reserve(version, r)
				if err == nil {
					model[id] = r
				}
			case 1: // modify
				if _, ok := model[id]; !ok {
					expectErr = ErrNotFound
				} else if !modelFits(snap, id, total, start, end, cap) {
					expectErr = ErrInsufficientCapacity
				}
				err = s.Modify(version, id, start, end, cap)
				if err == nil {
					model[id] = Reservation{ID: id, Start: start, End: end, Capacity: cap}
				}
			case 2: // cancel
				if _, ok := model[id]; !ok {
					expectErr = ErrNotFound
				}
				err = s.Cancel(version, id)
				if err == nil {
					delete(model, id)
				}
			}

			if (err == nil) != (expectErr == nil) {
				t.Fatalf("seed %d step %d: store err %v, model predicted %v", seed, step, err, expectErr)
			}
			if err == nil {
				version++
			}
			if s.Version() != version {
				t.Fatalf("seed %d step %d: version drift", seed, step)
			}

			got := s.Snapshot()
			if len(got) != len(model) {
				t.Fatalf("seed %d step %d: state size %d, model %d", seed, step, len(got), len(model))
			}
			for _, r := range got {
				if model[r.ID] != r {
					t.Fatalf("seed %d step %d: state mismatch for %s: %+v vs %+v", seed, step, r.ID, r, model[r.ID])
				}
			}
		}
	}
}
