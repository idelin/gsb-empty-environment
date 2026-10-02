package arbiter

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
)

// TestMutualExclusion hammers a single cell from many goroutines; the
// critical-section counter is only safe if ownership is truly exclusive.
// Run with -race.
func TestMutualExclusion(t *testing.T) {
	a := New()
	const workers = 16
	const rounds = 300
	const cell = ID(42)
	var counter int // guarded only by the grant
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			node := fmt.Sprintf("node-%d", w)
			for i := 0; i < rounds; i++ {
				g, err := a.AcquireTimeout(node, []ID{cell}, 5*time.Second)
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				v := counter
				counter = v + 1 // data race unless exclusion holds
				g.Release()
			}
		}(w)
	}
	wg.Wait()
	if counter != workers*rounds {
		t.Fatalf("counter = %d, want %d (exclusion broken)", counter, workers*rounds)
	}
	if a.Active() != 0 {
		t.Fatalf("active grants = %d, want 0", a.Active())
	}
}

// TestLinearizability records ownership intervals per cell and verifies
// they never overlap, i.e. every grant has a clean linearization point.
func TestLinearizability(t *testing.T) {
	a := New()
	const workers = 12
	const rounds = 120
	cells := []ID{1, 2, 3, 4}
	type interval struct {
		token      uint64
		start, end int64
	}
	var mu sync.Mutex
	history := map[ID][]interval{}
	var clock int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			node := fmt.Sprintf("n%d", w)
			for i := 0; i < rounds; i++ {
				c := cells[(w+i)%len(cells)]
				g, err := a.AcquireTimeout(node, []ID{c}, 5*time.Second)
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				mu.Lock()
				clock++
				start := clock
				mu.Unlock()
				time.Sleep(time.Microsecond * 50)
				mu.Lock()
				clock++
				history[c] = append(history[c], interval{g.token, start, clock})
				mu.Unlock()
				g.Release()
			}
		}(w)
	}
	wg.Wait()
	for c, ivs := range history {
		sort.Slice(ivs, func(i, j int) bool { return ivs[i].start < ivs[j].start })
		for i := 1; i < len(ivs); i++ {
			if ivs[i].start < ivs[i-1].end {
				t.Fatalf("cell %d: overlapping ownership %+v vs %+v",
					c, ivs[i-1], ivs[i])
			}
		}
	}
}

// TestAllOrNothing proves multi-cell acquisition is atomic: a failed
// attempt must leave every requested cell free.
func TestAllOrNothing(t *testing.T) {
	a := New()
	holder, ok := a.TryAcquire("holder", []ID{10})
	if !ok {
		t.Fatal("setup acquire failed")
	}
	defer holder.Release()

	done := make(chan error, 1)
	go func() {
		_, err := a.AcquireTimeout("blocked", []ID{10, 11}, 100*time.Millisecond)
		done <- err
	}()
	if err := <-done; err == nil {
		t.Fatal("expected timeout error for blocked acquisition")
	}
	// Cell 11 must never have been granted to the failed request.
	if g, ok := a.TryAcquire("checker", []ID{11}); !ok {
		t.Fatal("cell 11 leaked to failed acquisition")
	} else {
		g.Release()
	}
	if node, _, owned := a.OwnerOf(10); !owned || node != "holder" {
		t.Fatalf("cell 10 owner = %q owned=%v, want holder", node, owned)
	}
}

// TestTimeoutAndCancel covers context deadline and explicit cancellation.
func TestTimeoutAndCancel(t *testing.T) {
	a := New()
	holder, _ := a.TryAcquire("h", []ID{1})
	defer holder.Release()

	start := time.Now()
	if _, err := a.AcquireTimeout("w", []ID{1}, 50*time.Millisecond); err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := a.Acquire(ctx, "w2", []ID{1})
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-errCh; err != context.Canceled {
		t.Fatalf("cancel err = %v, want context.Canceled", err)
	}

	if _, err := a.AcquireTimeout("empty", nil, time.Second); err != ErrNoCells {
		t.Fatalf("empty set err = %v, want ErrNoCells", err)
	}
	if _, ok := a.TryAcquire("empty", nil); ok {
		t.Fatal("empty TryAcquire should fail")
	}
}

// TestStaleRelease ensures a released grant cannot free cells that were
// since re-granted to another node (exactly-once, token-validated).
func TestStaleRelease(t *testing.T) {
	a := New()
	g1, ok := a.TryAcquire("first", []ID{7})
	if !ok {
		t.Fatal("first acquire failed")
	}
	if !g1.Release() {
		t.Fatal("first release should succeed")
	}
	if g1.Release() {
		t.Fatal("second release must be a no-op")
	}
	g2, ok := a.TryAcquire("second", []ID{7})
	if !ok {
		t.Fatal("re-acquire failed")
	}
	if g1.Release() {
		t.Fatal("stale grant release must be a no-op")
	}
	if node, _, owned := a.OwnerOf(7); !owned || node != "second" {
		t.Fatalf("cell 7 owner = %q owned=%v, want second", node, owned)
	}
	g2.Release()
	if _, _, owned := a.OwnerOf(7); owned {
		t.Fatal("cell 7 should be free after real release")
	}
}

// TestFencingMonotonic verifies fencing tokens strictly increase per cell
// across successive owners.
func TestFencingMonotonic(t *testing.T) {
	a := New()
	var last uint64
	for i := 0; i < 50; i++ {
		g, ok := a.TryAcquire(fmt.Sprintf("n%d", i), []ID{9})
		if !ok {
			t.Fatal("acquire failed")
		}
		f, ok := g.Fence(9)
		if !ok {
			t.Fatal("missing fence for granted cell")
		}
		if f <= last {
			t.Fatalf("fence %d not increasing (last %d)", f, last)
		}
		last = f
		if _, ok := g.Fence(12345); ok {
			t.Fatal("fence returned for cell outside grant")
		}
		g.Release()
	}
}

// TestDedupAndSort confirms duplicate cells in a request are collapsed.
func TestDedupAndSort(t *testing.T) {
	a := New()
	g, ok := a.TryAcquire("n", []ID{5, 3, 5, 1, 3})
	if !ok {
		t.Fatal("acquire failed")
	}
	got := g.Cells()
	want := []ID{1, 3, 5}
	if len(got) != len(want) {
		t.Fatalf("cells = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cells = %v, want %v", got, want)
		}
	}
	g.Release()
}

// TestDeadlockFree randomizes overlapping multi-cell acquisitions; every
// goroutine must terminate, proving the lock-free-wait design cannot
// deadlock even under heavy contention.
func TestDeadlockFree(t *testing.T) {
	a := New()
	const workers = 24
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			node := fmt.Sprintf("n%d", w)
			for i := 0; i < 40; i++ {
				// Deterministic pseudo-random overlapping sets.
				base := (w*7 + i*13) % 8
				set := []ID{ID(base), ID((base + 1) % 8), ID((base + 3) % 8)}
				g, err := a.AcquireTimeout(node, set, 3*time.Second)
				if err != nil {
					continue // contention timeout is fine; deadlock is not
				}
				time.Sleep(time.Microsecond * 100)
				g.Release()
			}
		}(w)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("workers did not finish: deadlock suspected")
	}
	if a.Active() != 0 {
		t.Fatalf("active grants = %d, want 0", a.Active())
	}
}

// TestNoGoroutineLeak asserts that timed-out and cancelled waiters leave
// no goroutines or registered state behind.
func TestNoGoroutineLeak(t *testing.T) {
	a := New()
	holder, _ := a.TryAcquire("holder", []ID{1, 2, 3})

	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a.AcquireTimeout(fmt.Sprintf("w%d", i), []ID{1, 2}, 5*time.Millisecond)
			ctx, cancel := context.WithCancel(context.Background())
			go cancel()
			a.Acquire(ctx, fmt.Sprintf("c%d", i), []ID{3})
		}(i)
	}
	wg.Wait()
	holder.Release()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

// TestGrantAccessors covers the read-only grant surface.
func TestGrantAccessors(t *testing.T) {
	a := New()
	g, ok := a.TryAcquire("node-x", []ID{2, 1})
	if !ok {
		t.Fatal("acquire failed")
	}
	if g.Node() != "node-x" {
		t.Fatalf("node = %q", g.Node())
	}
	cells := g.Cells()
	cells[0] = 999 // mutating the copy must not affect the grant
	if g.Cells()[0] == 999 {
		t.Fatal("Cells() must return a copy")
	}
	if a.Active() != 1 {
		t.Fatalf("active = %d, want 1", a.Active())
	}
	g.Release()
	if a.Active() != 0 {
		t.Fatalf("active = %d, want 0", a.Active())
	}
}
