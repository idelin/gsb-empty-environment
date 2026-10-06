package budgetsem

import (
	"context"
	"sync"
	"testing"
	"time"
)

// gate is a controllable barrier: goroutines block on wait() until open().
type gate chan struct{}

func newGate() gate  { return make(chan struct{}) }
func (g gate) open() { close(g) }
func (g gate) wait() { <-g }
func (g gate) closed() bool {
	select {
	case <-g:
		return true
	default:
		return false
	}
}

// acquireResult carries the outcome of one Acquire call.
type acquireResult struct{ err error }

func acquireAsync(s *Semaphore, ctx context.Context, n int64) chan acquireResult {
	ch := make(chan acquireResult, 1)
	go func() { ch <- acquireResult{err: s.Acquire(ctx, n)} }()
	return ch
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func mustAcquire(t *testing.T, s *Semaphore, n int64) {
	t.Helper()
	if err := s.Acquire(context.Background(), n); err != nil {
		t.Fatalf("Acquire(%d): %v", n, err)
	}
}

func expectGranted(t *testing.T, what string, ch chan acquireResult) {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("%s: unexpected error: %v", what, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: timed out waiting for grant", what)
	}
}

func expectBlocked(t *testing.T, what string, ch chan acquireResult) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("%s: unexpectedly unblocked (err=%v)", what, r.err)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestBudgetConservation verifies that grants never exceed capacity and that
// all bytes return to the stock once released.
func TestBudgetConservation(t *testing.T) {
	const capacity = 64
	s := NewSemaphore(capacity)

	// Drain the budget in uneven pieces behind a barrier so all acquirers
	// race from the same starting line.
	start := newGate()
	granted := make(chan int64, 8)
	var wg sync.WaitGroup
	sizes := []int64{10, 20, 4, 30} // total == capacity
	for _, n := range sizes {
		wg.Add(1)
		go func(n int64) {
			defer wg.Done()
			start.wait()
			if err := s.Acquire(context.Background(), n); err != nil {
				t.Errorf("Acquire(%d): %v", n, err)
				return
			}
			granted <- n
		}(n)
	}
	start.open()
	wg.Wait()
	close(granted)

	var total int64
	for n := range granted {
		total += n
	}
	if total != capacity {
		t.Fatalf("granted %d bytes, want %d", total, capacity)
	}
	if got := s.Stock(); got != 0 {
		t.Fatalf("stock = %d, want 0 while budget fully granted", got)
	}

	// One more byte must block: the budget is exhausted.
	extra := acquireAsync(s, context.Background(), 1)
	expectBlocked(t, "over-budget acquire", extra)

	// Return everything; the extra byte becomes grantable exactly when the
	// last release lands.
	for _, n := range sizes {
		if err := s.Release(n); err != nil {
			t.Fatalf("Release(%d): %v", n, err)
		}
	}
	expectGranted(t, "over-budget acquire after releases", extra)
	if got := s.Stock(); got != capacity-1 {
		t.Fatalf("stock = %d, want %d", got, capacity-1)
	}
	if err := s.Release(1); err != nil {
		t.Fatal(err)
	}
	if got := s.Stock(); got != capacity {
		t.Fatalf("stock = %d, want %d after full repayment", got, capacity)
	}
}

// TestStrictFIFONoOvertaking verifies that a queued oversized request is
// never overtaken by a later small request, even when the small one fits.
func TestStrictFIFONoOvertaking(t *testing.T) {
	s := NewSemaphore(10)
	mustAcquire(t, s, 10) // drain the budget

	big := acquireAsync(s, context.Background(), 8) // queue head
	waitFor(t, "head queued", func() bool { return s.Waiters() == 1 })
	small := acquireAsync(s, context.Background(), 2) // would fit alone
	waitFor(t, "two queued waiters", func() bool { return s.Waiters() == 2 })

	// Free 2 bytes: the small request fits, but the big one is the head.
	if err := s.Release(2); err != nil {
		t.Fatal(err)
	}
	expectBlocked(t, "small request behind oversized head", small)
	expectBlocked(t, "oversized head without enough stock", big)

	// Free the remaining 8: the head is served first, then the small one.
	if err := s.Release(8); err != nil {
		t.Fatal(err)
	}
	expectGranted(t, "oversized head", big)
	expectGranted(t, "small request after head", small)

	if got := s.Stock(); got != 0 {
		t.Fatalf("stock = %d, want 0", got)
	}
}

// TestCancelHeadAdvancesQueue verifies that cancelling the queue head
// immediately lets the next waiter proceed.
func TestCancelHeadAdvancesQueue(t *testing.T) {
	s := NewSemaphore(10)
	mustAcquire(t, s, 10)

	ctx, cancel := context.WithCancel(context.Background())
	head := acquireAsync(s, ctx, 8)
	waitFor(t, "head queued", func() bool { return s.Waiters() == 1 })
	next := acquireAsync(s, context.Background(), 2)
	waitFor(t, "two queued waiters", func() bool { return s.Waiters() == 2 })

	// Free exactly what the follower needs; the head still blocks it.
	if err := s.Release(2); err != nil {
		t.Fatal(err)
	}
	expectBlocked(t, "follower behind cancelled-to-be head", next)

	// Cancel the head: the follower must advance without further releases.
	cancel()
	select {
	case r := <-head:
		if r.err != context.Canceled {
			t.Fatalf("head err = %v, want context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled head did not return")
	}
	expectGranted(t, "follower after head cancellation", next)

	if got := s.Waiters(); got != 0 {
		t.Fatalf("waiters = %d, want 0", got)
	}
	if got := s.Stock(); got != 0 {
		t.Fatalf("stock = %d, want 0", got)
	}
}

// TestGrantWinsOverLateCancel: once the grant is decided, a later cancel
// does not undo it — exactly one outcome, and it is the grant.
func TestGrantWinsOverLateCancel(t *testing.T) {
	s := NewSemaphore(4)
	mustAcquire(t, s, 4)

	ctx, cancel := context.WithCancel(context.Background())
	res := acquireAsync(s, ctx, 4)
	waitFor(t, "queued waiter", func() bool { return s.Waiters() == 1 })

	if err := s.Release(4); err != nil { // grant is decided here
		t.Fatal(err)
	}
	expectGranted(t, "granted waiter", res)
	cancel() // too late: the grant already won

	// The granted bytes are owned by the caller and must be repaid.
	if err := s.Release(4); err != nil {
		t.Fatal(err)
	}
	if got := s.Stock(); got != 4 {
		t.Fatalf("stock = %d, want 4", got)
	}
}

// TestCancelBeforeGrantLoses: a cancel that lands while still queued wins,
// and a later release must not resurrect the cancelled waiter.
func TestCancelBeforeGrantLoses(t *testing.T) {
	s := NewSemaphore(4)
	mustAcquire(t, s, 4)

	ctx, cancel := context.WithCancel(context.Background())
	res := acquireAsync(s, ctx, 4)
	waitFor(t, "queued waiter", func() bool { return s.Waiters() == 1 })

	cancel()
	select {
	case r := <-res:
		if r.err != context.Canceled {
			t.Fatalf("err = %v, want context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}

	if err := s.Release(4); err != nil {
		t.Fatal(err)
	}
	if got := s.Stock(); got != 4 {
		t.Fatalf("stock = %d, want 4; cancelled waiter must not be granted", got)
	}
	if got := s.Waiters(); got != 0 {
		t.Fatalf("waiters = %d, want 0", got)
	}
}

// TestGrantCancelRaceConservation fires a release and a cancel at the same
// waiter from behind a common barrier, many times. Whichever wins, the
// budget must remain conserved and the waiter must see exactly one outcome.
func TestGrantCancelRaceConservation(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		s := NewSemaphore(1)
		mustAcquire(t, s, 1)

		ctx, cancel := context.WithCancel(context.Background())
		res := acquireAsync(s, ctx, 1)
		waitFor(t, "queued waiter", func() bool { return s.Waiters() == 1 })

		start := newGate()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); start.wait(); s.Release(1) }()
		go func() { defer wg.Done(); start.wait(); cancel() }()
		start.open()
		wg.Wait()

		r := <-res
		if r.err == nil {
			// Grant won: the caller owns the byte and repays it.
			if err := s.Release(1); err != nil {
				t.Fatal(err)
			}
		} else if r.err != context.Canceled {
			t.Fatalf("iteration %d: unexpected err %v", i, r.err)
		}
		if got := s.Stock(); got != 1 {
			t.Fatalf("iteration %d: stock = %d, want 1 (budget leaked or duplicated)", i, got)
		}
		if got := s.Waiters(); got != 0 {
			t.Fatalf("iteration %d: waiters = %d, want 0", i, got)
		}
	}
}

// TestShrinkDoesNotRevokeAndAbsorbsExcess verifies that shrinking capacity
// leaves outstanding grants untouched, and that bytes returned beyond the
// new capacity are absorbed against the deficit instead of becoming
// grantable stock.
func TestShrinkDoesNotRevokeAndAbsorbsExcess(t *testing.T) {
	s := NewSemaphore(10)
	mustAcquire(t, s, 10) // outstanding grant larger than the future capacity

	s.SetCapacity(4)
	if got := s.Stock(); got != -6 {
		t.Fatalf("stock = %d, want -6 (deficit after shrink)", got)
	}

	// Nothing is grantable while the deficit is unpaid.
	blocked := acquireAsync(s, context.Background(), 1)
	waitFor(t, "queued waiter", func() bool { return s.Waiters() == 1 })
	expectBlocked(t, "acquire during deficit", blocked)

	// The outstanding grant is not revoked: its release repays the deficit
	// first, so only the new capacity (4) remains as stock — not 10.
	if err := s.Release(10); err != nil {
		t.Fatal(err)
	}
	expectGranted(t, "waiter after deficit repaid", blocked)
	if got := s.Stock(); got != 3 {
		t.Fatalf("stock = %d, want 3; excess stock must be absorbed", got)
	}

	// The shrunken budget is enforced: 4 fit, the 5th byte does not.
	mustAcquire(t, s, 3)
	extra := acquireAsync(s, context.Background(), 1)
	waitFor(t, "queued waiter", func() bool { return s.Waiters() == 1 })
	expectBlocked(t, "acquire beyond shrunken capacity", extra)
}

// TestShrinkIdleStock verifies that shrinking an idle semaphore returns the
// excess stock immediately.
func TestShrinkIdleStock(t *testing.T) {
	s := NewSemaphore(10)
	s.SetCapacity(4)
	if got := s.Stock(); got != 4 {
		t.Fatalf("stock = %d, want 4", got)
	}
	mustAcquire(t, s, 4)
	extra := acquireAsync(s, context.Background(), 1)
	waitFor(t, "queued waiter", func() bool { return s.Waiters() == 1 })
	expectBlocked(t, "acquire beyond shrunken capacity", extra)
}

// TestGrowCapacityUnblocksHead verifies that growing the capacity can
// release a previously unsatisfiable queue head.
func TestGrowCapacityUnblocksHead(t *testing.T) {
	s := NewSemaphore(4)
	big := acquireAsync(s, context.Background(), 6) // can never fit capacity 4
	waitFor(t, "queued waiter", func() bool { return s.Waiters() == 1 })
	expectBlocked(t, "oversized head", big)

	s.SetCapacity(8)
	expectGranted(t, "oversized head after growth", big)
	if got := s.Stock(); got != 2 {
		t.Fatalf("stock = %d, want 2", got)
	}
}

// TestInvalidSize rejects non-positive acquires and releases.
func TestInvalidSize(t *testing.T) {
	s := NewSemaphore(1)
	if err := s.Acquire(context.Background(), 0); err != ErrInvalidSize {
		t.Fatalf("Acquire(0) err = %v, want ErrInvalidSize", err)
	}
	if err := s.Release(0); err != ErrInvalidSize {
		t.Fatalf("Release(0) err = %v, want ErrInvalidSize", err)
	}
}
