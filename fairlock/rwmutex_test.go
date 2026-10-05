package fairlock

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitQueueLen blocks until the lock's internal queue has exactly n
// waiters. Tests use it to deterministically order goroutine steps:
// a waiter is enqueued under mu before it blocks, so observing the
// queue length is a reliable happens-before edge.
func waitQueueLen(t *testing.T, m *RWMutex, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		got := len(m.queue)
		m.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue length stuck at %d, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// mustNotBeGranted asserts that no result has been delivered on ch.
// It is deterministic when every state change that could grant the
// waiter is performed by the test goroutine itself: no other goroutine
// can produce a grant in between.
func mustNotBeGranted(t *testing.T, name string, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("%s was unexpectedly granted (err=%v)", name, err)
	default:
	}
}

func mustSucceed(t *testing.T, name string, ch <-chan error) {
	t.Helper()
	if err := <-ch; err != nil {
		t.Fatalf("%s: got %v, want nil", name, err)
	}
}

// TestWriterNotStarvedByLaterReaders: once a writer is queued, later
// readers queue behind it and cannot barge ahead.
func TestWriterNotStarvedByLaterReaders(t *testing.T) {
	var m RWMutex
	bg := context.Background()

	if err := m.RLock(bg); err != nil {
		t.Fatal(err)
	}

	w1 := make(chan error, 1)
	go func() { w1 <- m.Lock(bg) }()
	waitQueueLen(t, &m, 1)

	r2 := make(chan error, 1)
	go func() { r2 <- m.RLock(bg) }()
	waitQueueLen(t, &m, 2)

	m.RUnlock()
	// The writer is at the head: it must be granted first, and the
	// later reader must still be waiting.
	mustSucceed(t, "w1", w1)
	mustNotBeGranted(t, "r2", r2)

	m.Unlock()
	mustSucceed(t, "r2", r2)
	m.RUnlock()
}

// TestReaderBatchBeforeWriter: consecutive readers at the head of the
// queue are granted together, but a writer behind them waits.
func TestReaderBatchBeforeWriter(t *testing.T) {
	var m RWMutex
	bg := context.Background()

	if err := m.Lock(bg); err != nil {
		t.Fatal(err)
	}

	r1 := make(chan error, 1)
	go func() { r1 <- m.RLock(bg) }()
	waitQueueLen(t, &m, 1)
	r2 := make(chan error, 1)
	go func() { r2 <- m.RLock(bg) }()
	waitQueueLen(t, &m, 2)
	w1 := make(chan error, 1)
	go func() { w1 <- m.Lock(bg) }()
	waitQueueLen(t, &m, 3)

	m.Unlock()
	// Both readers are granted together; the writer stays queued.
	mustSucceed(t, "r1", r1)
	mustSucceed(t, "r2", r2)
	mustNotBeGranted(t, "w1", w1)

	m.RUnlock()
	mustNotBeGranted(t, "w1", w1) // one reader still active
	m.RUnlock()
	mustSucceed(t, "w1", w1)
	m.Unlock()
}

// TestDowngradeHasNoGap: a downgrading writer becomes a reader
// atomically; a queued writer is never granted in between, and later
// readers still queue fairly behind that writer.
func TestDowngradeHasNoGap(t *testing.T) {
	var m RWMutex
	bg := context.Background()

	if err := m.Lock(bg); err != nil {
		t.Fatal(err)
	}

	w2 := make(chan error, 1)
	go func() { w2 <- m.Lock(bg) }()
	waitQueueLen(t, &m, 1)

	m.Downgrade() // write -> read, atomically

	// If Downgrade had a lock-free gap, w2 could slip in here.
	mustNotBeGranted(t, "w2", w2)

	r3 := make(chan error, 1)
	go func() { r3 <- m.RLock(bg) }()
	waitQueueLen(t, &m, 2)
	// Fairness is preserved across the downgrade: r3 queues behind w2.
	mustNotBeGranted(t, "r3", r3)

	m.RUnlock() // release the downgraded read share
	mustSucceed(t, "w2", w2)
	m.Unlock()
	mustSucceed(t, "r3", r3)
	m.RUnlock()
}

// TestCancelHeadWriterNoLostWakeup: cancelling the head waiter must not
// strand the waiter behind it; the scheduler re-runs on cancellation.
func TestCancelHeadWriterNoLostWakeup(t *testing.T) {
	var m RWMutex
	bg := context.Background()

	if err := m.Lock(bg); err != nil {
		t.Fatal(err)
	}

	ctx1, cancel1 := context.WithCancel(bg)
	w1 := make(chan error, 1)
	go func() { w1 <- m.Lock(ctx1) }()
	waitQueueLen(t, &m, 1)

	w2 := make(chan error, 1)
	go func() { w2 <- m.Lock(bg) }()
	waitQueueLen(t, &m, 2)

	cancel1()
	if err := <-w1; err != context.Canceled {
		t.Fatalf("w1: got %v, want context.Canceled", err)
	}
	waitQueueLen(t, &m, 1)

	// If the cancellation lost the wakeup, w2 would block forever.
	m.Unlock()
	mustSucceed(t, "w2", w2)
	m.Unlock()
}

// TestCancelHeadReaderUnblocksWriter: same, with a cancelled reader at
// the head and a writer behind it.
func TestCancelHeadReaderUnblocksWriter(t *testing.T) {
	var m RWMutex
	bg := context.Background()

	if err := m.Lock(bg); err != nil {
		t.Fatal(err)
	}

	ctx1, cancel1 := context.WithCancel(bg)
	r1 := make(chan error, 1)
	go func() { r1 <- m.RLock(ctx1) }()
	waitQueueLen(t, &m, 1)

	w1 := make(chan error, 1)
	go func() { w1 <- m.Lock(bg) }()
	waitQueueLen(t, &m, 2)

	cancel1()
	if err := <-r1; err != context.Canceled {
		t.Fatalf("r1: got %v, want context.Canceled", err)
	}
	waitQueueLen(t, &m, 1)

	m.Unlock()
	mustSucceed(t, "w1", w1)
	m.Unlock()
}

// TestGrantWinsWhenLinearizedFirst: the grant is linearized (by Unlock)
// before the cancellation is processed, so the waiter ends up holding
// the lock even though its context is cancelled.
func TestGrantWinsWhenLinearizedFirst(t *testing.T) {
	var m RWMutex
	bg := context.Background()

	if err := m.Lock(bg); err != nil {
		t.Fatal(err)
	}

	ctx1, cancel1 := context.WithCancel(bg)
	w1 := make(chan error, 1)
	go func() {
		err := m.Lock(ctx1)
		if err == nil {
			m.Unlock() // w1 really holds the lock: release it
		}
		w1 <- err
	}()
	waitQueueLen(t, &m, 1)

	m.Unlock() // grant linearizes here, before the cancel below
	cancel1()  // cancel loses the tie-break

	mustSucceed(t, "w1", w1)
	waitQueueLen(t, &m, 0)

	// The lock must be free again (w1 held and released it).
	if err := m.Lock(bg); err != nil {
		t.Fatal(err)
	}
	m.Unlock()
}

// TestCancelWinsWhenLinearizedFirst: the cancellation is fully processed
// (the waiter is dequeued) before any grant, so the waiter returns
// ctx.Err() and holds nothing.
func TestCancelWinsWhenLinearizedFirst(t *testing.T) {
	var m RWMutex
	bg := context.Background()

	if err := m.Lock(bg); err != nil {
		t.Fatal(err)
	}

	ctx1, cancel1 := context.WithCancel(bg)
	w1 := make(chan error, 1)
	go func() { w1 <- m.Lock(ctx1) }()
	waitQueueLen(t, &m, 1)

	cancel1()
	if err := <-w1; err != context.Canceled {
		t.Fatalf("w1: got %v, want context.Canceled", err)
	}
	waitQueueLen(t, &m, 0) // cancel linearized: waiter dequeued

	m.Unlock()
	// w1 holds nothing; the lock is free for the next taker.
	if err := m.Lock(bg); err != nil {
		t.Fatal(err)
	}
	m.Unlock()
}

// TestFastPathIgnoresCancelledContext: an immediately grantable
// acquisition succeeds even with a cancelled context.
func TestFastPathIgnoresCancelledContext(t *testing.T) {
	var m RWMutex
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := m.Lock(ctx); err != nil {
		t.Fatalf("Lock: got %v, want nil", err)
	}
	m.Unlock()
	if err := m.RLock(ctx); err != nil {
		t.Fatalf("RLock: got %v, want nil", err)
	}
	m.RUnlock()
}

// TestMisusePanics: releasing or downgrading an unheld lock panics.
func TestMisusePanics(t *testing.T) {
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not panic", name)
			}
		}()
		f()
	}
	var m RWMutex
	mustPanic("Unlock", m.Unlock)
	mustPanic("RUnlock", m.RUnlock)
	mustPanic("Downgrade", m.Downgrade)
}

// TestStressExclusion hammers the lock with writers, readers, downgrades
// and cancellations, asserting the exclusion invariants inside every
// critical section. Run with -race.
//
// The counters mirror the lock state: curWriters is incremented after
// Lock and decremented before Unlock/Downgrade; curReaders is
// incremented after RLock/Downgrade and decremented before RUnlock.
// Because a counter is always brought to its "token held" value before
// the token is released, a reader that observes curWriters != 0 or a
// writer that observes curReaders != 0 is a genuine overlap.
func TestStressExclusion(t *testing.T) {
	var m RWMutex
	bg := context.Background()

	var curWriters, curReaders, violations int32
	fail := func(msg string) {
		atomic.AddInt32(&violations, 1)
		t.Error(msg)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err := m.Lock(bg); err != nil {
					fail("Lock: " + err.Error())
					return
				}
				if atomic.AddInt32(&curWriters, 1) != 1 {
					fail("two writers hold the lock")
				}
				if atomic.LoadInt32(&curReaders) != 0 {
					fail("writer overlaps readers")
				}
				if i%3 == 0 {
					// Downgrade: release the writer counter first, then
					// convert; the lock-level transition is atomic.
					atomic.AddInt32(&curWriters, -1)
					m.Downgrade()
					atomic.AddInt32(&curReaders, 1)
					atomic.AddInt32(&curReaders, -1)
					m.RUnlock()
				} else {
					atomic.AddInt32(&curWriters, -1)
					m.Unlock()
				}
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ctx := bg
				var cancel context.CancelFunc
				if i%5 == 0 {
					ctx, cancel = context.WithTimeout(bg, time.Microsecond)
				}
				err := m.RLock(ctx)
				if cancel != nil {
					cancel()
				}
				if err != nil {
					continue // cancelled: holds nothing
				}
				if atomic.LoadInt32(&curWriters) != 0 {
					fail("reader overlaps writer")
				}
				atomic.AddInt32(&curReaders, 1)
				atomic.AddInt32(&curReaders, -1)
				m.RUnlock()
			}
		}(g)
	}
	wg.Wait()

	if atomic.LoadInt32(&violations) != 0 {
		t.Fatalf("%d exclusion violations", violations)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writing || m.readers != 0 || len(m.queue) != 0 {
		t.Fatalf("lock not quiescent: writing=%v readers=%d queued=%d",
			m.writing, m.readers, len(m.queue))
	}
}
