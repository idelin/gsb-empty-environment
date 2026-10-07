package seq_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"seq"
	"seq/fault"
)

func mustNext(t *testing.T, a *seq.Allocator) uint64 {
	t.Helper()
	id, err := a.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return id
}

// Basic segment allocation: ids are contiguous within and across segments,
// and a flush happens exactly once per segment.
func TestSegmentsAndExhaustion(t *testing.T) {
	mem := seq.NewMemStore()
	fs := fault.New(mem)
	a, err := seq.NewAllocator(fs, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < 10; i++ {
		if id := mustNext(t, a); id != i {
			t.Fatalf("got %d, want %d", id, i)
		}
	}
	// 10 ids with step 3 -> reserves at 0,3,6,9 => 4 flushes.
	if got := fs.Calls(); got != 4 {
		t.Fatalf("flushes = %d, want 4", got)
	}
	st, _ := mem.Load()
	if st.SegmentEnd != 12 {
		t.Fatalf("SegmentEnd = %d, want 12", st.SegmentEnd)
	}
}

// Flush failure: the failed Next must not issue any ID, and the allocator
// must recover and keep its monotonic guarantee once the store heals.
func TestFlushFailureDoesNotIssue(t *testing.T) {
	mem := seq.NewMemStore()
	fs := fault.New(mem, fault.Fault{Call: 1, Kind: fault.FailBeforeFlush})
	a, _ := seq.NewAllocator(fs, 5)

	if _, err := a.Next(); err == nil {
		t.Fatal("expected flush error")
	}
	if st, _ := mem.Load(); st.SegmentEnd != 0 {
		t.Fatalf("store mutated despite failed flush: %+v", st)
	}
	// Recovers on retry; the first issued ID is still 0 (nothing was burned).
	if id := mustNext(t, a); id != 0 {
		t.Fatalf("got %d, want 0", id)
	}
}

// Ambiguous flush: the write landed but the caller saw an error. The
// allocator must not reuse the reserved segment; the retry reserves a fresh
// one, so the ambiguously-flushed segment is skipped entirely.
func TestAmbiguousFlushSkipsSegment(t *testing.T) {
	mem := seq.NewMemStore()
	fs := fault.New(mem, fault.Fault{Call: 1, Kind: fault.FailAfterFlush})
	a, _ := seq.NewAllocator(fs, 5)

	if _, err := a.Next(); err == nil {
		t.Fatal("expected flush error")
	}
	// The write did land (end=5), but the allocator never used that segment.
	if id := mustNext(t, a); id != 5 {
		t.Fatalf("got %d, want 5 (segment [0,5) skipped)", id)
	}
}

// Crash before flush: the reservation never landed, so nothing was reserved
// and nothing was issued. A restarted allocator starts from the same point;
// no returned ID is ever reused because none was returned.
func TestCrashBeforeFlush(t *testing.T) {
	mem := seq.NewMemStore()
	fs := fault.New(mem, fault.Fault{Call: 1, Kind: fault.CrashBeforeFlush})
	a, _ := seq.NewAllocator(fs, 5)

	if _, err := a.Next(); err != fault.ErrCrash {
		t.Fatalf("got %v, want ErrCrash", err)
	}
	if st, _ := mem.Load(); st.SegmentEnd != 0 {
		t.Fatalf("store mutated despite crash before flush: %+v", st)
	}
	// Restart.
	b, _ := seq.NewAllocator(mem, 5)
	if id := mustNext(t, b); id != 0 {
		t.Fatalf("got %d, want 0", id)
	}
}

// Crash after flush, before any ID from the segment was used: the restart
// skips the whole orphaned segment.
func TestCrashAfterFlushBeforeUse(t *testing.T) {
	mem := seq.NewMemStore()
	fs := fault.New(mem, fault.Fault{Call: 1, Kind: fault.CrashAfterFlush})
	a, _ := seq.NewAllocator(fs, 5)

	if _, err := a.Next(); err != fault.ErrCrash {
		t.Fatalf("got %v, want ErrCrash", err)
	}
	// Restart: segment [0,5) was persisted but never used -> skipped.
	b, _ := seq.NewAllocator(mem, 5)
	if id := mustNext(t, b); id != 5 {
		t.Fatalf("got %d, want 5", id)
	}
}

// Crash mid-stream: some IDs were returned, then the process crashed while
// reserving the next segment (after the flush landed). Restart must skip
// both the unused tail of the old segment and the whole orphaned segment,
// and must never reissue a returned ID.
func TestCrashAfterFlushMidStream(t *testing.T) {
	mem := seq.NewMemStore()
	fs := fault.New(mem, fault.Fault{Call: 2, Kind: fault.CrashAfterFlush})
	a, _ := seq.NewAllocator(fs, 4)

	// Reserve #1 (call 1) serves ids 0..3 from segment [0,4).
	var returned []uint64
	for i := 0; i < 4; i++ {
		returned = append(returned, mustNext(t, a))
	}
	// The 5th draw triggers reserve #2 (call 2): the flush lands
	// (persisting end=8) and then the process crashes before using it.
	if _, err := a.Next(); err != fault.ErrCrash {
		t.Fatalf("got %v, want ErrCrash", err)
	}
	if st, _ := mem.Load(); st.SegmentEnd != 8 {
		t.Fatalf("SegmentEnd = %d, want 8", st.SegmentEnd)
	}
	// Restart: segment [4,8) was persisted but never used -> skipped, and
	// returned ids 0..3 are never reissued.
	b, _ := seq.NewAllocator(mem, 4)
	for i := 0; i < 4; i++ {
		id := mustNext(t, b)
		if id < 8 {
			t.Fatalf("id %d below persisted high-water 8", id)
		}
		for _, r := range returned {
			if id == r {
				t.Fatalf("id %d reissued after restart", id)
			}
		}
	}
}

// Restart with a live (unfaulted) allocator: unused tail of the previous
// segment is skipped, returned IDs are never reused.
func TestRestartSkipsTail(t *testing.T) {
	mem := seq.NewMemStore()
	a, _ := seq.NewAllocator(mem, 10)
	first := mustNext(t, a) // 0
	second := mustNext(t, a)

	b, _ := seq.NewAllocator(mem, 10)
	id := mustNext(t, b)
	if id != 10 {
		t.Fatalf("got %d, want 10 (tail 2..9 skipped)", id)
	}
	if id == first || id == second {
		t.Fatal("reused a returned ID")
	}
}

// Many randomized crash/restart cycles: across the whole run, every returned
// ID is globally unique and each post-restart ID exceeds every ID returned
// before the crash.
func TestRestartNeverReusesReturnedIDs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, crashKind := range []fault.Kind{fault.CrashBeforeFlush, fault.CrashAfterFlush} {
		t.Run(fmt.Sprint(crashKind), func(t *testing.T) {
			mem := seq.NewMemStore()
			var all []uint64
			maxBefore := uint64(0)
			for cycle := 0; cycle < 50; cycle++ {
				// Inject a crash at a random future reservation.
				at := rng.Intn(3) + 1
				fs := fault.New(mem, fault.Fault{Call: at, Kind: crashKind})
				a, _ := seq.NewAllocator(fs, uint64(rng.Intn(8)+1))
				for {
					id, err := a.Next()
					if err == fault.ErrCrash {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if id < maxBefore {
						t.Fatalf("id %d < pre-crash max %d", id, maxBefore)
					}
					all = append(all, id)
				}
				if len(all) > 0 {
					maxBefore = all[len(all)-1]
					for _, id := range all {
						if id > maxBefore {
							maxBefore = id
						}
					}
				}
			}
			seen := make(map[uint64]bool)
			for _, id := range all {
				if seen[id] {
					t.Fatalf("id %d returned twice", id)
				}
				seen[id] = true
			}
		})
	}
}

// Concurrent allocators (modelling separate processes) sharing one store:
// all issued IDs are globally unique.
func TestConcurrentAllocatorsNoOverlap(t *testing.T) {
	mem := seq.NewMemStore()
	const workers = 4
	const perWorker = 500

	var wg sync.WaitGroup
	results := make([][]uint64, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			a, _ := seq.NewAllocator(mem, 7)
			for i := 0; i < perWorker; i++ {
				id, err := a.Next()
				if err != nil {
					t.Error(err)
					return
				}
				results[w] = append(results[w], id)
			}
		}(w)
	}
	wg.Wait()

	seen := make(map[uint64]bool)
	for _, ids := range results {
		if len(ids) != perWorker {
			t.Fatalf("worker returned %d ids", len(ids))
		}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("id %d issued twice", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("got %d unique ids, want %d", len(seen), workers*perWorker)
	}
}

// Single allocator under concurrent callers.
func TestConcurrentNext(t *testing.T) {
	a, _ := seq.NewAllocator(seq.NewMemStore(), 3)
	const n = 1000
	ids := make([]uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := a.Next()
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()
	seen := make(map[uint64]bool)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("id %d issued twice", id)
		}
		seen[id] = true
	}
}

func TestInvalidStep(t *testing.T) {
	if _, err := seq.NewAllocator(seq.NewMemStore(), 0); err != seq.ErrInvalidStep {
		t.Fatalf("got %v, want ErrInvalidStep", err)
	}
}
