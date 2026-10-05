package bufpool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// barrier is a reusable arrival barrier for interleaving goroutines.
type barrier struct {
	mu    sync.Mutex
	cond  *sync.Cond
	n     int
	count int
	gen   int
}

func newBarrier(n int) *barrier {
	b := &barrier{n: n}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *barrier) await() {
	b.mu.Lock()
	b.count++
	if b.count == b.n {
		b.count = 0
		b.gen++
		b.cond.Broadcast()
	} else {
		gen := b.gen
		for gen == b.gen {
			b.cond.Wait()
		}
	}
	b.mu.Unlock()
}

func mustAcquire(t *testing.T, p *Pool, length, capacity int, writable bool) *Handle {
	t.Helper()
	h, err := p.Acquire(length, capacity, writable)
	if err != nil {
		t.Fatalf("Acquire(%d,%d): %v", length, capacity, err)
	}
	return h
}

func mustVerify(t *testing.T, p *Pool) {
	t.Helper()
	if err := p.Ledger().VerifyPool(p); err != nil {
		t.Fatalf("ledger verify: %v", err)
	}
}

// TestBarrierInterleavedLifecycle interleaves borrow, handoff, retain,
// release and reuse across goroutines using arrival barriers.
func TestBarrierInterleavedLifecycle(t *testing.T) {
	p := NewPool(4096)
	bar := newBarrier(3)
	handoff := make(chan *Handle, 1)
	errs := make(chan error, 3)

	// Goroutine A: borrow writable, write, hand off to B.
	go func() {
		h, err := p.Acquire(64, 128, true)
		if err != nil {
			errs <- err
			return
		}
		bar.await() // phase 1: all borrowed
		m, err := h.MutableBytes()
		if err != nil {
			errs <- err
			return
		}
		for i := range m {
			m[i] = byte(i)
		}
		handoff <- h // transfer ownership to B
		bar.await()  // phase 2: handoff done
		bar.await()  // phase 3: B released, C re-borrowed
		errs <- nil
	}()

	// Goroutine B: borrow its own buffer, then receive A's handoff and release it.
	go func() {
		h, err := p.Acquire(32, 64, false)
		if err != nil {
			errs <- err
			return
		}
		bar.await() // phase 1
		other := <-handoff
		view, err := other.Bytes()
		if err != nil {
			errs <- err
			return
		}
		if view[63] != 63 {
			errs <- fmt.Errorf("handoff data corrupted: view[63]=%d", view[63])
			return
		}
		if err := other.Release(); err != nil {
			errs <- err
			return
		}
		bar.await() // phase 2
		if err := h.Release(); err != nil {
			errs <- err
			return
		}
		bar.await() // phase 3
		errs <- nil
	}()

	// Goroutine C: wait for handoff release, then re-borrow the same buffer.
	go func() {
		bar.await() // phase 1
		bar.await() // phase 2: A's buffer is idle again
		h, err := p.Acquire(16, 128, true)
		if err != nil {
			errs <- err
			return
		}
		if h.Generation() < 2 {
			errs <- fmt.Errorf("reused buffer generation = %d, want >= 2", h.Generation())
		}
		if err := h.Release(); err != nil {
			errs <- err
			return
		}
		bar.await() // phase 3
		errs <- nil
	}()

	for i := 0; i < 3; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	mustVerify(t, p)
}

// TestDoubleRelease ensures a second release is an error and changes nothing.
func TestDoubleRelease(t *testing.T) {
	p := NewPool(1024)
	h := mustAcquire(t, p, 8, 16, true)
	if err := h.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := h.Release(); !errors.Is(err, ErrReleased) {
		t.Fatalf("second release = %v, want ErrReleased", err)
	}
	s := p.Ledger().Snapshot()
	if s.Entries[h.ID()].Refs != 0 {
		t.Fatalf("refs after double release = %d, want 0", s.Entries[h.ID()].Refs)
	}
	mustVerify(t, p)
}

// TestStaleGeneration ensures old-generation handles cannot read, write,
// retain or release after the buffer is re-borrowed.
func TestStaleGeneration(t *testing.T) {
	p := NewPool(1024)
	old := mustAcquire(t, p, 8, 16, true)
	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	fresh := mustAcquire(t, p, 8, 16, true)
	if fresh.ID() != old.ID() {
		t.Fatalf("expected reuse of buffer %d, got %d", old.ID(), fresh.ID())
	}
	if fresh.Generation() != old.Generation()+1 {
		t.Fatalf("generation %d -> %d, want +1", old.Generation(), fresh.Generation())
	}
	if _, err := old.Bytes(); !errors.Is(err, ErrStale) {
		t.Errorf("stale Bytes = %v, want ErrStale", err)
	}
	if _, err := old.MutableBytes(); !errors.Is(err, ErrStale) {
		t.Errorf("stale MutableBytes = %v, want ErrStale", err)
	}
	if _, err := old.Retain(); !errors.Is(err, ErrStale) {
		t.Errorf("stale Retain = %v, want ErrStale", err)
	}
	if err := old.Release(); !errors.Is(err, ErrStale) {
		t.Errorf("stale Release = %v, want ErrStale", err)
	}
	// The fresh borrow's refcount must be untouched by stale attempts.
	if got := p.Ledger().Snapshot().Entries[fresh.ID()].Refs; got != 1 {
		t.Fatalf("fresh refs = %d, want 1", got)
	}
	if err := fresh.Release(); err != nil {
		t.Fatal(err)
	}
	mustVerify(t, p)
}

// TestSharedReadOnlyForbidsMutation: while retained read-only borrows are
// outstanding, mutable access is denied; retained handles cannot write.
func TestSharedReadOnlyForbidsMutation(t *testing.T) {
	p := NewPool(1024)
	h := mustAcquire(t, p, 8, 16, true)
	ro, err := h.Retain()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.MutableBytes(); !errors.Is(err, ErrShared) {
		t.Fatalf("MutableBytes with shared readers = %v, want ErrShared", err)
	}
	if _, err := ro.MutableBytes(); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("MutableBytes via retained handle = %v, want ErrNotWritable", err)
	}
	if _, err := ro.Bytes(); err != nil {
		t.Fatalf("read-only Bytes: %v", err)
	}
	if err := ro.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.MutableBytes(); err != nil {
		t.Fatalf("MutableBytes after readers gone: %v", err)
	}
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	mustVerify(t, p)
}

// TestPartialAcquireFailure: the budget covers only part of a batch; the
// failed portion must not alter any reference counts.
func TestPartialAcquireFailure(t *testing.T) {
	p := NewPool(3 * 100)
	keep := mustAcquire(t, p, 10, 100, false) // refs=1 on buffer 1
	before := p.Ledger().Snapshot()

	handles, err := p.AcquireMany(5, 10, 100, false)
	if !errors.Is(err, ErrNoBudget) {
		t.Fatalf("AcquireMany err = %v, want ErrNoBudget", err)
	}
	if len(handles) != 2 {
		t.Fatalf("acquired %d handles, want 2", len(handles))
	}
	after := p.Ledger().Snapshot()
	if after.Entries[keep.ID()].Refs != before.Entries[keep.ID()].Refs {
		t.Fatalf("unrelated buffer refs changed: %d -> %d",
			before.Entries[keep.ID()].Refs, after.Entries[keep.ID()].Refs)
	}
	if after.TotalCapacity != 3*100 {
		t.Fatalf("billed capacity = %d, want 300", after.TotalCapacity)
	}
	// One more acquire must fail and bill nothing.
	if _, err := p.Acquire(1, 1, false); !errors.Is(err, ErrNoBudget) {
		t.Fatalf("over-budget Acquire = %v, want ErrNoBudget", err)
	}
	if got := p.Stats().UsedBytes; got != 300 {
		t.Fatalf("used bytes after failed acquire = %d, want 300", got)
	}
	for _, h := range handles {
		if err := h.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if err := keep.Release(); err != nil {
		t.Fatal(err)
	}
	mustVerify(t, p)
}

// TestAcquireWaitCancellation: a waiter blocked on budget wakes up on context
// cancellation, and a waiter is unblocked by a release.
func TestAcquireWaitCancellation(t *testing.T) {
	p := NewPool(100)
	h := mustAcquire(t, p, 10, 100, true)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := p.AcquireWait(ctx, 10, 100, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireWait = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancelation took %v", elapsed)
	}

	// A release unblocks a waiter.
	got := make(chan *Handle, 1)
	go func() {
		wh, err := p.AcquireWait(context.Background(), 10, 100, false)
		if err == nil {
			got <- wh
		}
	}()
	time.Sleep(20 * time.Millisecond)
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case wh := <-got:
		if err := wh.Release(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not unblocked by release")
	}
	mustVerify(t, p)
}

// TestClearReleasesIdleOnly: Clear frees idle capacity; in-use buffers remain
// billed and usable, and return to the idle pool when released.
func TestClearReleasesIdleOnly(t *testing.T) {
	p := NewPool(400)
	inUse := mustAcquire(t, p, 10, 100, true)
	idleBuf := mustAcquire(t, p, 10, 100, false)
	if err := idleBuf.Release(); err != nil {
		t.Fatal(err)
	}

	p.Clear()
	s := p.Stats()
	if s.UsedBytes != 100 || s.IdleBytes != 0 {
		t.Fatalf("after Clear: used=%d idle=%d, want 100/0", s.UsedBytes, s.IdleBytes)
	}
	ls := p.Ledger().Snapshot()
	if _, ok := ls.Entries[idleBuf.ID()]; ok {
		t.Fatalf("cleared buffer %d still in ledger", idleBuf.ID())
	}
	// In-use buffer still readable/writable.
	if _, err := inUse.MutableBytes(); err != nil {
		t.Fatalf("in-use buffer after Clear: %v", err)
	}
	if err := inUse.Release(); err != nil {
		t.Fatal(err)
	}
	if got := p.Stats().IdleBytes; got != 100 {
		t.Fatalf("released buffer not back to idle: idle=%d", got)
	}
	mustVerify(t, p)
}

// TestWritableBorrowsDoNotAlias: two live writable borrows never share
// backing memory, and a buffer is recyclable only after refs hit zero.
func TestWritableBorrowsDoNotAlias(t *testing.T) {
	p := NewPool(1 << 20)
	a := mustAcquire(t, p, 256, 256, true)
	b := mustAcquire(t, p, 256, 256, true)
	if a.ID() == b.ID() {
		t.Fatal("two live writable borrows share a physical buffer")
	}
	ma, _ := a.MutableBytes()
	mb, _ := b.MutableBytes()
	for i := range ma {
		ma[i] = 0xAA
	}
	for i := range mb {
		if mb[i] != 0 {
			t.Fatalf("write to buffer %d visible in buffer %d at %d", a.ID(), b.ID(), i)
		}
	}
	if &ma[0] == &mb[0] {
		t.Fatal("backing arrays alias")
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	// Refs zero -> recyclable: next acquire of fitting size reuses a's buffer.
	c := mustAcquire(t, p, 128, 256, true)
	if c.ID() != a.ID() {
		t.Fatalf("buffer %d not recycled, got %d", a.ID(), c.ID())
	}
	_ = c.Release()
	_ = b.Release()
	mustVerify(t, p)
}

// TestConcurrentStress hammers the pool from many goroutines; run with -race.
func TestConcurrentStress(t *testing.T) {
	p := NewPool(64 * 1024)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				h, err := p.Acquire(64, 128, true)
				if err != nil {
					continue
				}
				ro, err := h.Retain()
				if err != nil {
					t.Errorf("retain: %v", err)
					return
				}
				if _, err := h.MutableBytes(); !errors.Is(err, ErrShared) {
					t.Errorf("mutable with shared reader = %v", err)
					return
				}
				if _, err := ro.Bytes(); err != nil {
					t.Errorf("read: %v", err)
					return
				}
				if err := ro.Release(); err != nil {
					t.Errorf("release ro: %v", err)
					return
				}
				m, err := h.MutableBytes()
				if err != nil {
					t.Errorf("mutable after readers gone: %v", err)
					return
				}
				m[0] = byte(g)
				if err := h.Release(); err != nil {
					t.Errorf("release: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	mustVerify(t, p)
}

// TestAllocationCount reports explainable allocation counts: warm-pool
// borrow/release cycles should allocate only the handle, and physical buffer
// allocations happen exactly once per recycled buffer.
func TestAllocationCount(t *testing.T) {
	p := NewPool(1 << 20)
	h := mustAcquire(t, p, 128, 256, true)
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(1000, func() {
		wh, err := p.Acquire(128, 256, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := wh.Release(); err != nil {
			t.Fatal(err)
		}
	})
	ls := p.Ledger().Snapshot()
	t.Logf("allocs per warm borrow/release cycle: %.2f (handle only; backing array reused)", allocs)
	t.Logf("physical buffers: allocs=%d frees=%d live=%d", ls.Allocs, ls.Frees, ls.LivePhysicalCount)
	t.Logf("ledger ops: borrows=%d retains=%d releases=%d", ls.Borrows, ls.Retains, ls.Releases)
	if allocs > 2 {
		t.Fatalf("warm cycle allocs = %.2f, want <= 2 (handle + slice header)", allocs)
	}
	if ls.Allocs != 1 {
		t.Fatalf("physical allocs = %d, want 1 (single recycled buffer)", ls.Allocs)
	}
	mustVerify(t, p)
}
