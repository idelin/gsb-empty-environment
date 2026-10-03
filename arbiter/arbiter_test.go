package arbiter

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hexgsb/hexgrid"
)

func testCells(n int) []hexgrid.Cell {
	base, err := hexgrid.FromLatLng(hexgrid.LatLng{Lat: 39.9042, Lng: 116.4074}, 8)
	if err != nil {
		panic(err)
	}
	k := 0
	for 1+3*k*(k+1) < n {
		k++
	}
	disk, err := base.GridDisk(k)
	if err != nil {
		panic(err)
	}
	return disk[:n]
}

func TestAcquireReleaseBasic(t *testing.T) {
	a := New()
	cells := testCells(7)
	fence, err := a.Acquire("node-a", cells, 0)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if fence == 0 {
		t.Fatal("fence must be non-zero")
	}
	snap, err := a.Snapshot(cells)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != len(cells) {
		t.Fatalf("snapshot shows %d claims, want %d", len(snap), len(cells))
	}
	for _, c := range cells {
		if snap[c].Owner != "node-a" || snap[c].Fence != fence {
			t.Fatalf("bad claim on %s: %+v", c, snap[c])
		}
	}
	if err := a.Release("node-a", cells, fence); err != nil {
		t.Fatalf("release: %v", err)
	}
	snap, _ = a.Snapshot(cells)
	if len(snap) != 0 {
		t.Fatalf("expected no claims after release, got %d", len(snap))
	}
}

func TestEmptyAndDuplicateAndTooMany(t *testing.T) {
	a := New(WithMaxCells(4))
	if _, err := a.Acquire("n", nil, 0); !errors.Is(err, ErrEmptyClaim) {
		t.Fatalf("want ErrEmptyClaim, got %v", err)
	}
	if err := a.Release("n", nil, 0); !errors.Is(err, ErrEmptyClaim) {
		t.Fatalf("want ErrEmptyClaim, got %v", err)
	}
	cells := testCells(3)
	dup := []hexgrid.Cell{cells[0], cells[1], cells[2], cells[0], cells[1]}
	if _, err := a.Acquire("n", dup, 0); err != nil {
		t.Fatalf("duplicates must be deduped: %v", err)
	}
	snap, _ := a.Snapshot(dup)
	if len(snap) != 3 {
		t.Fatalf("want 3 claims after dedupe, got %d", len(snap))
	}
	if _, err := a.Acquire("n", testCells(5), 0); !errors.Is(err, ErrTooManyCells) {
		t.Fatalf("want ErrTooManyCells, got %v", err)
	}
}

func TestConflictAtomicity(t *testing.T) {
	a := New()
	cells := testCells(5)
	if _, err := a.Acquire("holder", cells[2:3], 0); err != nil {
		t.Fatal(err)
	}
	_, err := a.Acquire("contender", cells, 0)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConflictError, got %v", err)
	}
	if len(ce.Held) != 1 || ce.Held[cells[2]] != "holder" {
		t.Fatalf("bad conflict report: %+v", ce.Held)
	}
	// All-or-nothing: contender must own nothing, holder keeps its cell.
	snap, _ := a.Snapshot(cells)
	if len(snap) != 1 || snap[cells[2]].Owner != "holder" {
		t.Fatalf("atomicity violated: %+v", snap)
	}
	// Re-acquiring one's own cells is a renew, not a conflict.
	if _, err := a.Acquire("holder", cells[2:3], 0); err != nil {
		t.Fatalf("self re-acquire must succeed: %v", err)
	}
	// But the contender still cannot take them.
	if _, err := a.Acquire("contender", cells[:3], 0); err == nil {
		t.Fatal("expected conflict for contender on holder cells")
	}
}

func TestConcurrentSingleCell(t *testing.T) {
	a := New()
	cell := testCells(1)
	const nodes = 32
	var wins int64
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if _, err := a.Acquire(fmt.Sprintf("node-%d", id), cell, 0); err == nil {
				atomic.AddInt64(&wins, 1)
			}
		}(n)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins)
	}
	snap, _ := a.Snapshot(cell)
	if len(snap) != 1 {
		t.Fatalf("claims = %d, want 1", len(snap))
	}
}

func TestReleaseValidation(t *testing.T) {
	a := New()
	cells := testCells(3)
	fence, err := a.Acquire("owner", cells, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Release("intruder", cells, 0); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("want ErrNotOwner, got %v", err)
	}
	if err := a.Release("owner", cells, fence+99); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("want ErrStaleFence, got %v", err)
	}
	if err := a.Release("owner", cells, fence); err != nil {
		t.Fatal(err)
	}
	if err := a.Release("owner", cells, fence); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("double release: want ErrNotHeld, got %v", err)
	}
	other := testCells(7)[5:]
	if err := a.Release("owner", other, 0); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("release of never-held: want ErrNotHeld, got %v", err)
	}
}

func TestFencingMonotonicAndStale(t *testing.T) {
	a := New()
	cells := testCells(2)
	f1, err := a.Acquire("n", cells, 0)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := a.Renew("n", cells, f1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f2 <= f1 {
		t.Fatalf("fence not increasing: %d -> %d", f1, f2)
	}
	if err := a.Release("n", cells, f1); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("old fence must be stale after renew: %v", err)
	}
	if err := a.Release("n", cells, f2); err != nil {
		t.Fatal(err)
	}
	f3, err := a.Acquire("n", cells, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f3 <= f2 {
		t.Fatalf("fence not increasing across acquires: %d -> %d", f2, f3)
	}
}

func TestLeaseExpiry(t *testing.T) {
	now := time.Now()
	clock := &now
	a := New(WithClock(func() time.Time { return *clock }))
	cells := testCells(2)
	if _, err := a.Acquire("old", cells, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Acquire("new", cells, 0); err == nil {
		t.Fatal("acquire must fail while lease is active")
	}
	*clock = now.Add(2 * time.Minute)
	if _, err := a.Acquire("new", cells, 0); err != nil {
		t.Fatalf("acquire must succeed after expiry: %v", err)
	}
	if err := a.Release("old", cells, 0); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("evicted owner: want ErrNotOwner, got %v", err)
	}
	if _, err := a.Renew("old", cells, 0, time.Minute); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("evicted renew: want ErrNotOwner, got %v", err)
	}
	snap, _ := a.Snapshot(cells)
	if len(snap) != len(cells) {
		t.Fatalf("claims = %d, want %d", len(snap), len(cells))
	}
	for _, info := range snap {
		if info.Owner != "new" {
			t.Fatalf("owner = %q, want new", info.Owner)
		}
	}
}

// TestLinearizableOwnership hammers a single cell from many nodes and
// asserts that ownership intervals never overlap (the linearizability
// witness: at most one node holds the cell at any instant).
func TestLinearizableOwnership(t *testing.T) {
	a := New()
	cell := testCells(1)
	const nodes = 8
	const rounds = 500
	var holding int32
	var wg sync.WaitGroup
	fail := make(chan string, 1)
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("node-%d", id)
			for r := 0; r < rounds; r++ {
				fence, err := a.Acquire(name, cell, 0)
				if err != nil {
					continue
				}
				if !atomic.CompareAndSwapInt32(&holding, 0, 1) {
					select {
					case fail <- "overlapping ownership detected":
					default:
					}
					return
				}
				owner, ok := a.OwnerOf(cell[0])
				if !ok || owner != name {
					select {
					case fail <- fmt.Sprintf("snapshot owner %q != %q", owner, name):
					default:
					}
					return
				}
				atomic.StoreInt32(&holding, 0)
				if err := a.Release(name, cell, fence); err != nil {
					select {
					case fail <- fmt.Sprintf("release: %v", err):
					default:
					}
					return
				}
			}
		}(n)
	}
	wg.Wait()
	select {
	case msg := <-fail:
		t.Fatal(msg)
	default:
	}
}

// TestConcurrentOverlappingClaims stresses multi-cell atomic claims with
// heavy overlap to prove deadlock-freedom and all-or-nothing semantics.
func TestConcurrentOverlappingClaims(t *testing.T) {
	a := New()
	universe := testCells(37)
	const nodes = 16
	const rounds = 100
	var wg sync.WaitGroup
	wg.Add(nodes)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	for n := 0; n < nodes; n++ {
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			name := fmt.Sprintf("node-%d", id)
			for r := 0; r < rounds; r++ {
				// Random overlapping subset.
				set := make([]hexgrid.Cell, 0, 8)
				for _, c := range universe {
					if rng.Intn(8) == 0 {
						set = append(set, c)
					}
				}
				if len(set) == 0 {
					set = append(set, universe[rng.Intn(len(universe))])
				}
				fence, err := a.Acquire(name, set, 0)
				if err != nil {
					continue
				}
				// Atomicity witness: right after a successful acquire,
				// a consistent snapshot must show us owning every cell.
				snap, serr := a.Snapshot(set)
				if serr != nil {
					t.Errorf("snapshot: %v", serr)
					return
				}
				for _, c := range set {
					if snap[c].Owner != name {
						t.Errorf("atomicity violated: %s not owned by %s after acquire", c, name)
						return
					}
				}
				if err := a.Release(name, set, fence); err != nil {
					t.Errorf("release: %v", err)
					return
				}
			}
		}(n)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock suspected: workload did not finish in 30s")
	}
	// After everything, no claims may remain.
	snap, err := a.Snapshot(universe)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 0 {
		t.Fatalf("leaked claims: %+v", snap)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	// Warm up runtime internals, then take a baseline.
	runtime.Gosched()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	a := New()
	universe := testCells(19)
	var wg sync.WaitGroup
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("node-%d", id)
			for r := 0; r < 50; r++ {
				set := universe[id : id+3]
				if fence, err := a.Acquire(name, set, 0); err == nil {
					_ = a.Release(name, set, fence)
				}
			}
		}(n)
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: baseline %d, now %d", baseline, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
