package seq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

var errCrash = errors.New("injected crash")

func mustGenerator(t *testing.T, store Store, key string, step uint64) *Generator {
	t.Helper()
	g, err := NewGenerator(store, key, step)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	return g
}

func mustNext(t *testing.T, g *Generator) uint64 {
	t.Helper()
	id, err := g.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return id
}

func persistedMax(t *testing.T, store Store, key string) uint64 {
	t.Helper()
	max, err := store.MaxReserved(context.Background(), key)
	if err != nil {
		t.Fatalf("MaxReserved: %v", err)
	}
	return max
}

// Segment reservation amortizes flushes: the store is touched once per
// segment, not once per number.
func TestSegmentReservationAmortizesFlush(t *testing.T) {
	store := NewMemoryStore()
	g := mustGenerator(t, store, "order", 3)

	for want := uint64(1); want <= 6; want++ {
		if got := mustNext(t, g); got != want {
			t.Fatalf("Next = %d, want %d", got, want)
		}
		var wantMax uint64
		if want <= 3 {
			wantMax = 3
		} else {
			wantMax = 6
		}
		if max := persistedMax(t, store, "order"); max != wantMax {
			t.Fatalf("after id %d persisted max = %d, want %d", want, max, wantMax)
		}
	}
}

// Exhausting a segment triggers exactly one new reservation, and the
// new segment starts right above the old one.
func TestSegmentExhaustionTriggersNewReservation(t *testing.T) {
	store := NewMemoryStore()
	g := mustGenerator(t, store, "order", 2)

	first := mustNext(t, g)
	second := mustNext(t, g)
	if first != 1 || second != 2 {
		t.Fatalf("got %d,%d want 1,2", first, second)
	}
	// Third call crosses the segment boundary.
	if got := mustNext(t, g); got != 3 {
		t.Fatalf("got %d want 3", got)
	}
	if max := persistedMax(t, store, "order"); max != 4 {
		t.Fatalf("persisted max = %d, want 4", max)
	}
}

// Crash before flush: the reservation is lost, Next reports an error
// and issues nothing. After recovery the sequence continues without
// reusing any number.
func TestCrashBeforeFlush(t *testing.T) {
	store := NewFaultStore(NewMemoryStore())
	g := mustGenerator(t, store, "order", 2)

	id1 := mustNext(t, g)
	id2 := mustNext(t, g)

	// Next call must reserve; crash before the write lands.
	store.SetFault(func(string, uint64, uint64) error { return errCrash }, nil, nil)
	if _, err := g.Next(context.Background()); err == nil {
		t.Fatal("Next succeeded despite failed reservation flush")
	}
	if max := persistedMax(t, store, "order"); max != 2 {
		t.Fatalf("persisted max = %d, want 2 (write must not be applied)", max)
	}

	// Recover: the sequence continues above the old reservation.
	store.SetFault(nil, nil, nil)
	id3 := mustNext(t, g)
	if id3 <= id2 || id3 <= id1 {
		t.Fatalf("id %d reuses or goes backwards after crash (issued %d,%d)", id3, id1, id2)
	}
	if id3 != 3 {
		t.Fatalf("id = %d, want 3", id3)
	}
}

// Crash after flush: the write landed but the caller saw an error, so
// nothing was issued. A restarted generator must skip the whole
// reserved segment rather than reuse it.
func TestCrashAfterFlush(t *testing.T) {
	store := NewFaultStore(NewMemoryStore())
	g := mustGenerator(t, store, "order", 5)

	issued := map[uint64]bool{}
	for i := 0; i < 5; i++ {
		issued[mustNext(t, g)] = true
	}

	// The reservation for the next segment is persisted, then the
	// process "crashes" before the caller learns the outcome.
	store.SetFault(nil, func(string, uint64, uint64) error { return errCrash }, nil)
	if _, err := g.Next(context.Background()); err == nil {
		t.Fatal("Next succeeded despite post-flush crash")
	}
	if max := persistedMax(t, store, "order"); max != 10 {
		t.Fatalf("persisted max = %d, want 10 (write was applied)", max)
	}

	// Restart with a healthy store: numbers 6..10 are skipped, never
	// reused, even though nobody issued them.
	store.SetFault(nil, nil, nil)
	g2 := mustGenerator(t, store, "order", 5)
	for i := 0; i < 5; i++ {
		id := mustNext(t, g2)
		if id <= 10 {
			t.Fatalf("restarted generator returned %d, must be > 10", id)
		}
		if issued[id] {
			t.Fatalf("restarted generator reused id %d", id)
		}
		issued[id] = true
	}
}

// Restart mid-segment: unissued numbers from the previous segment are
// skipped (gap), never reused.
func TestRestartSkipsUnissuedNumbers(t *testing.T) {
	store := NewMemoryStore()
	g := mustGenerator(t, store, "order", 100)

	first := mustNext(t, g)
	if first != 1 {
		t.Fatalf("first id = %d, want 1", first)
	}

	// Simulate process crash: drop the generator, keep the store.
	g2 := mustGenerator(t, store, "order", 100)
	id := mustNext(t, g2)
	if id != 101 {
		t.Fatalf("after restart id = %d, want 101 (2..100 skipped)", id)
	}
}

// Cross-process coordination: many generators share one store through
// the CAS abstraction; every issued number is unique and covered by a
// persisted reservation.
func TestConcurrentGeneratorsNeverReuse(t *testing.T) {
	store := NewMemoryStore()
	const workers = 8
	const perWorker = 200

	var mu sync.Mutex
	issued := make(map[uint64]bool)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := NewGenerator(store, "shared", 7)
			if err != nil {
				t.Errorf("NewGenerator: %v", err)
				return
			}
			for i := 0; i < perWorker; i++ {
				id, err := g.Next(context.Background())
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				mu.Lock()
				if issued[id] {
					t.Errorf("duplicate id %d", id)
				}
				issued[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(issued) != workers*perWorker {
		t.Fatalf("issued %d unique ids, want %d", len(issued), workers*perWorker)
	}
	max := persistedMax(t, store, "shared")
	for id := range issued {
		if id > max {
			t.Fatalf("id %d issued beyond persisted reservation %d", id, max)
		}
	}
}

// A generator that cannot load the high-water mark must not issue.
func TestLoadFailureIssuesNothing(t *testing.T) {
	store := NewFaultStore(NewMemoryStore())
	store.SetFault(nil, nil, errCrash)

	g := mustGenerator(t, store, "order", 4)
	if _, err := g.Next(context.Background()); err == nil {
		t.Fatal("Next succeeded despite load failure")
	}

	store.SetFault(nil, nil, nil)
	if id := mustNext(t, g); id != 1 {
		t.Fatalf("id = %d, want 1 after recovery", id)
	}
}

// A CAS loser reloads the moved high-water mark and retries instead of
// reusing numbers.
func TestCASConflictRetry(t *testing.T) {
	inner := NewMemoryStore()
	g1 := mustGenerator(t, inner, "order", 10)
	g2 := mustGenerator(t, inner, "order", 10)

	// g1 reserves [1,10]; g2 loads max=0 and its first CAS loses.
	if id := mustNext(t, g1); id != 1 {
		t.Fatalf("g1 id = %d, want 1", id)
	}
	id := mustNext(t, g2)
	if id <= 10 {
		t.Fatalf("g2 id = %d, want > 10 after conflict retry", id)
	}
	if id != 11 {
		t.Fatalf("g2 id = %d, want 11", id)
	}
}

// Sequence space exhaustion is reported, not wrapped around.
func TestExhaustion(t *testing.T) {
	store := NewMemoryStore()
	if err := store.CompareAndSwapMaxReserved(context.Background(), "order", 0, ^uint64(0)-1); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	g := mustGenerator(t, store, "order", 10)
	if _, err := g.Next(context.Background()); err != ErrExhausted {
		t.Fatalf("Next err = %v, want ErrExhausted", err)
	}
}

// Repeated crash/recover cycles across restarts never reuse a number.
func TestCrashStormNeverReuses(t *testing.T) {
	store := NewFaultStore(NewMemoryStore())
	issued := make(map[uint64]bool)
	var highest uint64

	for cycle := 0; cycle < 20; cycle++ {
		g := mustGenerator(t, store, "order", 4)
		for i := 0; i < 6; i++ {
			// Randomly crash before or after the flush on some cycles.
			switch cycle % 3 {
			case 1:
				store.SetFault(func(string, uint64, uint64) error { return errCrash }, nil, nil)
			case 2:
				store.SetFault(nil, func(string, uint64, uint64) error { return errCrash }, nil)
			}
			id, err := g.Next(context.Background())
			store.SetFault(nil, nil, nil)
			if err != nil {
				continue
			}
			if issued[id] {
				t.Fatalf("cycle %d: reused id %d", cycle, id)
			}
			if id <= highest {
				t.Fatalf("cycle %d: id %d not above previous max %d", cycle, id, highest)
			}
			issued[id] = true
			highest = id
		}
	}
	if max := persistedMax(t, store, "order"); highest > max {
		t.Fatalf("issued id %d beyond persisted max %d", highest, max)
	}
}

// Example of reserving a segment per key with a small step.
func ExampleGenerator_Next() {
	store := NewMemoryStore()
	g, _ := NewGenerator(store, "order", 100)
	id, _ := g.Next(context.Background())
	fmt.Println(id)
	// Output: 1
}
