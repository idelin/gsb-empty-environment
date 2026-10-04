package bufpool_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"bufpool"
)

// ---------------------------------------------------------------------------
// Independent ledger: tracks physical buffer identity purely through the
// public observer API and cross-checks the pool's accounting.
// ---------------------------------------------------------------------------

type ledger struct {
	mu       sync.Mutex
	live     map[uint64]int64 // physical ID -> charged capacity bytes
	gens     map[uint64]uint64
	allocs   int
	frees    int
	borrows  int
	recycled int // borrows of a previously seen physical buffer
	idles    int
	releases int
	bad      []string
}

func newLedger() *ledger {
	return &ledger{live: map[uint64]int64{}, gens: map[uint64]uint64{}}
}

func (l *ledger) observe(e bufpool.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch e.Kind {
	case bufpool.EventAlloc:
		if _, dup := l.live[e.ID]; dup {
			l.bad = append(l.bad, fmt.Sprintf("capacity double-charged: buffer %d allocated while still live", e.ID))
		}
		l.live[e.ID] = int64(e.Cap)
		l.allocs++
	case bufpool.EventFree:
		if _, ok := l.live[e.ID]; !ok {
			l.bad = append(l.bad, fmt.Sprintf("uncharge of untracked buffer %d", e.ID))
		}
		delete(l.live, e.ID)
		l.frees++
	case bufpool.EventBorrow:
		if _, ok := l.live[e.ID]; !ok {
			l.bad = append(l.bad, fmt.Sprintf("borrow of uncharged buffer %d", e.ID))
		}
		if prev, seen := l.gens[e.ID]; seen {
			if e.Gen <= prev {
				l.bad = append(l.bad, fmt.Sprintf("buffer %d generation did not increase: %d -> %d", e.ID, prev, e.Gen))
			}
			l.recycled++
		}
		l.gens[e.ID] = e.Gen
		l.borrows++
	case bufpool.EventIdle:
		if _, ok := l.live[e.ID]; !ok {
			l.bad = append(l.bad, fmt.Sprintf("idle of uncharged buffer %d", e.ID))
		}
		l.idles++
	case bufpool.EventRelease:
		l.releases++
	}
}

func (l *ledger) chargedSum() int64 {
	var sum int64
	for _, c := range l.live {
		sum += c
	}
	return sum
}

// check verifies the ledger has no violations and agrees with pool stats.
func (l *ledger) check(t *testing.T, p *bufpool.Pool) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, msg := range l.bad {
		t.Errorf("ledger violation: %s", msg)
	}
	st := p.Stats()
	if got := l.chargedSum(); got != st.CapBytes {
		t.Errorf("ledger charged %d != pool CapBytes %d", got, st.CapBytes)
	}
	if st.CapBytes != st.IdleCapBytes+st.UseCapBytes {
		t.Errorf("CapBytes %d != idle %d + in-use %d", st.CapBytes, st.IdleCapBytes, st.UseCapBytes)
	}
	if st.CapBytes > st.MaxCapBytes {
		t.Errorf("CapBytes %d exceeds budget %d", st.CapBytes, st.MaxCapBytes)
	}
}

func (l *ledger) counts() (allocs, frees, borrows, recycled int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.allocs, l.frees, l.borrows, l.recycled
}

// ---------------------------------------------------------------------------
// Cyclic barrier for deterministic interleavings.
// ---------------------------------------------------------------------------

type barrier struct {
	mu    sync.Mutex
	n     int
	count int
	ch    chan struct{}
}

func newBarrier(n int) *barrier { return &barrier{n: n, ch: make(chan struct{})} }

func (b *barrier) await() {
	b.mu.Lock()
	b.count++
	if b.count == b.n {
		b.count = 0
		close(b.ch)
		b.ch = make(chan struct{})
		b.mu.Unlock()
		return
	}
	ch := b.ch
	b.mu.Unlock()
	<-ch
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDoubleRelease(t *testing.T) {
	p := bufpool.NewPool(64)
	led := newLedger()
	p.SetObserver(led.observe)

	h, err := p.Borrow(context.Background(), 32)
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.Retain()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	// Repeated release of the consumed handle must fail and must not
	// decrement the refcount again.
	if err := h.Release(); !errors.Is(err, bufpool.ErrStale) {
		t.Fatalf("second release: got %v, want ErrStale", err)
	}
	if st := p.Stats(); st.UseBuffers != 1 || st.IdleBuffers != 0 {
		t.Fatalf("refcount corrupted by double release: %+v", st)
	}
	// The retained handle keeps the buffer checked out exactly once.
	if err := r.Release(); err != nil {
		t.Fatal(err)
	}
	if st := p.Stats(); st.UseBuffers != 0 || st.IdleBuffers != 1 {
		t.Fatalf("buffer not recycled exactly once: %+v", st)
	}
	led.check(t, p)
}

func TestStaleHandleAfterReborrow(t *testing.T) {
	p := bufpool.NewPool(64)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	h1, err := p.Borrow(ctx, 16)
	if err != nil {
		t.Fatal(err)
	}
	id1, gen1 := h1.PhysicalID(), h1.Generation()
	if err := h1.Release(); err != nil {
		t.Fatal(err)
	}

	h2, err := p.Borrow(ctx, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Release()
	if h2.PhysicalID() != id1 {
		t.Fatalf("expected physical buffer %d to be reused, got %d", id1, h2.PhysicalID())
	}
	if h2.Generation() <= gen1 {
		t.Fatalf("generation not bumped on re-borrow: %d -> %d", gen1, h2.Generation())
	}

	// The old handle belongs to a dead generation: every operation fails.
	if _, err := h1.View(); !errors.Is(err, bufpool.ErrStale) {
		t.Errorf("stale View: %v", err)
	}
	if _, err := h1.Bytes(); !errors.Is(err, bufpool.ErrStale) {
		t.Errorf("stale Bytes: %v", err)
	}
	if _, err := h1.Retain(); !errors.Is(err, bufpool.ErrStale) {
		t.Errorf("stale Retain: %v", err)
	}
	if _, err := h1.Transfer(); !errors.Is(err, bufpool.ErrStale) {
		t.Errorf("stale Transfer: %v", err)
	}
	if err := h1.Release(); !errors.Is(err, bufpool.ErrStale) {
		t.Errorf("stale Release: %v", err)
	}
	// The live generation is unaffected by stale traffic.
	if _, err := h2.Bytes(); err != nil {
		t.Fatalf("live handle broken by stale traffic: %v", err)
	}
	led.check(t, p)
}

func TestReadOnlySharingForbidsMutation(t *testing.T) {
	p := bufpool.NewPool(128)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	w1, err := p.Borrow(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	id1 := w1.PhysicalID()
	b, _ := w1.Bytes()
	for i := range b {
		b[i] = 0xA0
	}

	r1, err := w1.Downgrade()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Bytes(); !errors.Is(err, bufpool.ErrStale) {
		t.Fatalf("downgraded write handle still usable: %v", err)
	}
	r2, err := r1.Retain()
	if err != nil {
		t.Fatal(err)
	}

	// Shared read-only borrow: no mutation path may succeed.
	if _, err := r1.Bytes(); !errors.Is(err, bufpool.ErrMode) {
		t.Errorf("Bytes on read handle: %v", err)
	}
	if _, err := r1.Upgrade(); !errors.Is(err, bufpool.ErrShared) {
		t.Errorf("Upgrade while shared: %v", err)
	}
	// A second borrow is a different physical buffer, never an alias.
	w2, err := p.Borrow(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	if w2.PhysicalID() == id1 {
		t.Fatal("distinct write borrows alias the same physical buffer")
	}
	b2, _ := w2.Bytes()
	for i := range b2 {
		b2[i] = 0x0B
	}
	v, _ := r1.View()
	for i, x := range v {
		if x != 0xA0 {
			t.Fatalf("read-only view corrupted at %d: %#x", i, x)
		}
	}

	// Releasing one reader still leaves the buffer shared.
	if err := r2.Release(); err != nil {
		t.Fatal(err)
	}
	w1b, err := r1.Upgrade()
	if err != nil {
		t.Fatalf("sole reader failed to upgrade: %v", err)
	}
	bb, _ := w1b.Bytes()
	bb[0] = 0xFF
	if v, _ := w1b.View(); v[0] != 0xFF {
		t.Fatal("write through upgraded handle not visible")
	}
	if err := w1b.Release(); err != nil {
		t.Fatal(err)
	}
	if err := w2.Release(); err != nil {
		t.Fatal(err)
	}
	led.check(t, p)
}

func TestPartialAllocationFailure(t *testing.T) {
	p := bufpool.NewPool(100)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	a, err := p.Borrow(ctx, 60)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Borrow(ctx, 40)
	if err != nil {
		t.Fatal(err)
	}
	before := p.Stats()

	// Budget exhausted: the next request fails...
	if _, err := p.TryBorrow(1); !errors.Is(err, bufpool.ErrWouldBlock) {
		t.Fatalf("TryBorrow past budget: %v", err)
	}
	// ...and an unsatisfiable request fails immediately.
	if _, err := p.Borrow(ctx, 101); !errors.Is(err, bufpool.ErrOversize) {
		t.Fatalf("oversize Borrow: %v", err)
	}
	// Failed requests changed no accounting and no refcounts.
	if after := p.Stats(); after != before {
		t.Fatalf("failed borrow mutated accounting: %+v -> %+v", before, after)
	}
	r, err := a.Retain()
	if err != nil {
		t.Fatalf("refcount damaged by failed borrow: %v", err)
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.View(); err != nil {
		t.Fatalf("retained handle lost its borrow: %v", err)
	}
	if err := r.Release(); err != nil {
		t.Fatal(err)
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	if st.CapBytes != 100 || st.IdleCapBytes != 100 || st.UseCapBytes != 0 || st.LenBytes != 0 {
		t.Fatalf("unexpected final accounting: %+v", st)
	}
	led.check(t, p)
}

func TestCancelWaiting(t *testing.T) {
	p := bufpool.NewPool(64)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	h, err := p.Borrow(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	before := p.Stats()

	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := p.Borrow(wctx, 32)
		done <- err
	}()
	// The waiter is parked: nothing it does may disturb accounting.
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: got %v, want context.Canceled", err)
	}
	if after := p.Stats(); after != before {
		t.Fatalf("cancelled wait mutated accounting: %+v -> %+v", before, after)
	}

	// A waiter is also woken by a release, not only by cancellation.
	got := make(chan *bufpool.Handle, 1)
	go func() {
		h, err := p.Borrow(ctx, 64)
		if err != nil {
			done <- err
			return
		}
		got <- h
	}()
	time.Sleep(20 * time.Millisecond)
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case h2 := <-got:
		if err := h2.Release(); err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("woken waiter failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not woken by release")
	}
	led.check(t, p)
}

func TestTransfer(t *testing.T) {
	p := bufpool.NewPool(64)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	h, err := p.Borrow(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	id := h.PhysicalID()
	h2, err := h.Transfer()
	if err != nil {
		t.Fatal(err)
	}
	// The source handle is consumed; the borrow itself is untouched.
	if err := h.Release(); !errors.Is(err, bufpool.ErrStale) {
		t.Errorf("release of transferred-away handle: %v", err)
	}
	if _, err := h.View(); !errors.Is(err, bufpool.ErrStale) {
		t.Errorf("view through transferred-away handle: %v", err)
	}
	if h2.PhysicalID() != id {
		t.Fatal("transfer changed physical identity")
	}
	if st := p.Stats(); st.UseBuffers != 1 {
		t.Fatalf("transfer disturbed refcount: %+v", st)
	}
	if err := h2.Release(); err != nil {
		t.Fatal(err)
	}
	// The borrow was returned exactly once: the buffer is recyclable.
	h3, err := p.Borrow(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if h3.PhysicalID() != id {
		t.Fatalf("buffer %d not recycled after refcount reached zero", id)
	}
	if err := h3.Release(); err != nil {
		t.Fatal(err)
	}
	led.check(t, p)
}

func TestClearOnlyFreesIdle(t *testing.T) {
	p := bufpool.NewPool(128)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	a, err := p.Borrow(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Borrow(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Release(); err != nil { // a idle, b in use
		t.Fatal(err)
	}
	p.Clear()
	st := p.Stats()
	if st.CapBytes != 64 || st.IdleCapBytes != 0 || st.UseCapBytes != 64 {
		t.Fatalf("Clear touched in-use buffer: %+v", st)
	}
	// The in-use buffer is still fully usable.
	bb, _ := b.Bytes()
	bb[0] = 7
	if v, _ := b.View(); v[0] != 7 {
		t.Fatal("in-use buffer corrupted by Clear")
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if st := p.Stats(); st.CapBytes != 64 || st.IdleCapBytes != 64 {
		t.Fatalf("released buffer not returned to idle pool: %+v", st)
	}
	p.Clear()
	if st := p.Stats(); st.CapBytes != 0 || st.IdleBuffers != 0 {
		t.Fatalf("second Clear did not drain idle pool: %+v", st)
	}
	led.check(t, p)
	if allocs, frees, _, _ := led.counts(); allocs != frees {
		t.Fatalf("ledger leak: %d allocs vs %d frees", allocs, frees)
	}
}

func TestLengthAndCapacityBilledSeparately(t *testing.T) {
	p := bufpool.NewPool(256)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	h, err := p.Borrow(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	// Re-borrow a shorter length: capacity stays 64, length is 8.
	h2, err := p.Borrow(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	if h2.Len() != 8 || h2.Cap() != 64 {
		t.Fatalf("len/cap conflated: len=%d cap=%d", h2.Len(), h2.Cap())
	}
	st := p.Stats()
	if st.CapBytes != 64 || st.UseCapBytes != 64 || st.LenBytes != 8 {
		t.Fatalf("length and capacity not billed separately: %+v", st)
	}
	if err := h2.Release(); err != nil {
		t.Fatal(err)
	}
	if st := p.Stats(); st.LenBytes != 0 || st.CapBytes != 64 || st.IdleCapBytes != 64 {
		t.Fatalf("release mis-billed: %+v", st)
	}
	led.check(t, p)
}

// TestBarrierInterleaved hammers the pool from several goroutines whose
// borrow / transfer / retain / release / reuse steps are interleaved by a
// cyclic barrier, then reconciles the ledger with the final accounting.
func TestBarrierInterleaved(t *testing.T) {
	const goroutines = 4
	const rounds = 50
	p := bufpool.NewPool(goroutines * 64)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	bar := newBarrier(goroutines)
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				h, err := p.Borrow(ctx, 64)
				if err != nil {
					errs <- err
					return
				}
				// Hand the borrow to a fresh handle mid-flight.
				h, err = h.Transfer()
				if err != nil {
					errs <- err
					return
				}
				extra, err := h.Retain()
				if err != nil {
					errs <- err
					return
				}
				bar.await() // every goroutine holds its borrow
				b, err := h.Bytes()
				if err != nil {
					errs <- err
					return
				}
				pat := byte(g + 1)
				for i := range b {
					b[i] = pat
				}
				bar.await() // writes complete; verify no cross-talk
				for i, x := range b {
					if x != pat {
						errs <- fmt.Errorf("g%d round %d: byte %d = %#x, want %#x (aliasing?)", g, r, i, x, pat)
						return
					}
				}
				if err := extra.Release(); err != nil {
					errs <- err
					return
				}
				if err := h.Release(); err != nil {
					errs <- err
					return
				}
				bar.await() // buffers idle before the next round reuses them
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	st := p.Stats()
	if st.UseBuffers != 0 || st.LenBytes != 0 || st.IdleBuffers != goroutines {
		t.Fatalf("pool not quiescent after interleaving: %+v", st)
	}
	led.check(t, p)
	allocs, _, borrows, recycled := led.counts()
	if borrows != goroutines*rounds {
		t.Fatalf("borrows = %d, want %d", borrows, goroutines*rounds)
	}
	if recycled != borrows-allocs {
		t.Fatalf("recycled = %d, want borrows-allocs = %d", recycled, borrows-allocs)
	}
	if allocs > goroutines {
		t.Fatalf("allocated %d physical buffers, budget allows steady-state of %d", allocs, goroutines)
	}
	p.Clear()
	if allocs2, frees, _, _ := led.counts(); allocs2 != frees {
		t.Fatalf("after Clear: %d allocs vs %d frees", allocs2, frees)
	}
}

// TestNoAliasingBetweenWriteBorrows fills the pool exactly and verifies
// that simultaneously held write borrows are pairwise distinct physical
// buffers and never observe each other's writes.
func TestNoAliasingBetweenWriteBorrows(t *testing.T) {
	const n = 6
	const size = 48
	p := bufpool.NewPool(n * size)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	bar := newBarrier(n)
	handles := make([]*bufpool.Handle, n)
	ids := make([]uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := p.Borrow(ctx, size)
			if err != nil {
				t.Error(err)
				return
			}
			handles[i] = h
			ids[i] = h.PhysicalID()
			bar.await() // all borrows held simultaneously
			b, _ := h.Bytes()
			for j := range b {
				b[j] = byte(i + 1)
			}
			bar.await() // all writes done
			v, _ := h.View()
			for j, x := range v {
				if x != byte(i+1) {
					t.Errorf("handle %d observed foreign byte at %d: %#x", i, j, x)
					break
				}
			}
		}(i)
	}
	wg.Wait()
	seen := map[uint64]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("physical buffer %d handed to two live write borrows", id)
		}
		seen[id] = true
	}
	for _, h := range handles {
		if err := h.Release(); err != nil {
			t.Fatal(err)
		}
	}
	led.check(t, p)
}

// TestAllocationCountReport runs a fixed, explainable workload and reports
// the physical allocation count, which must equal the steady-state working
// set: every borrow after warm-up recycles a refcount-zero buffer.
func TestAllocationCountReport(t *testing.T) {
	const workers = 3
	const rounds = 40
	p := bufpool.NewPool(workers * 32)
	led := newLedger()
	p.SetObserver(led.observe)
	ctx := context.Background()

	bar := newBarrier(workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				h, err := p.Borrow(ctx, 32)
				if err != nil {
					t.Error(err)
					return
				}
				bar.await() // all workers hold a borrow simultaneously
				if err := h.Release(); err != nil {
					t.Error(err)
					return
				}
				bar.await() // all borrows idle before the next round
			}
		}()
	}
	wg.Wait()

	allocs, _, borrows, recycled := led.counts()
	// With 3 workers and budget for exactly 3 buffers, the pool must
	// allocate exactly 3 physical buffers and recycle every later borrow.
	if allocs != workers {
		t.Fatalf("physical allocs = %d, want exactly %d (working set)", allocs, workers)
	}
	if recycled != borrows-allocs {
		t.Fatalf("recycled = %d, want %d", recycled, borrows-allocs)
	}
	t.Logf("allocation report: %d borrows served by %d physical buffers (%d allocations, %d recycles)",
		borrows, allocs, allocs, recycled)
	led.check(t, p)
}
