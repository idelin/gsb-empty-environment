package fairlock

import (
	"context"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

var bg = context.Background()

// waitFor polls cond until it holds or the deadline passes. Tests use
// it to synchronize on internal lock state instead of sleeping, which
// keeps the interleavings deterministic.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		runtime.Gosched()
	}
}

func (m *RWMutex) state() (readers int, writer bool, queued int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readers, m.writer, m.queue.Len()
}

func mustLock(t *testing.T, m *RWMutex) {
	t.Helper()
	if err := m.Lock(bg); err != nil {
		t.Fatalf("Lock: %v", err)
	}
}

func mustRLock(t *testing.T, m *RWMutex) {
	t.Helper()
	if err := m.RLock(bg); err != nil {
		t.Fatalf("RLock: %v", err)
	}
}

func TestWriteExclusion(t *testing.T) {
	var m RWMutex
	var mu sync.Mutex
	counter := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if err := m.Lock(bg); err != nil {
					t.Errorf("Lock: %v", err)
					return
				}
				mu.Lock()
				counter++
				cur := counter
				mu.Unlock()
				runtime.Gosched()
				mu.Lock()
				if counter != cur {
					t.Errorf("counter changed under write lock")
				}
				mu.Unlock()
				m.Unlock()
			}
		}()
	}
	wg.Wait()
}

func TestReadersCoexist(t *testing.T) {
	var m RWMutex
	mustRLock(t, &m)
	mustRLock(t, &m)
	r, w, q := m.state()
	if r != 2 || w || q != 0 {
		t.Fatalf("state = (%d, %v, %d), want (2, false, 0)", r, w, q)
	}
	m.RUnlock()
	m.RUnlock()
}

// TestWriterNotStarvedByReaders: a queued writer blocks later readers,
// and is granted as soon as the current readers drain.
func TestWriterNotStarvedByReaders(t *testing.T) {
	var m RWMutex
	mustRLock(t, &m) // active reader

	wDone := make(chan error, 1)
	go func() { wDone <- m.Lock(bg) }()
	waitFor(t, "writer queued", func() bool {
		_, _, q := m.state()
		return q == 1
	})

	// A reader arriving after the writer queued must queue behind it.
	rDone := make(chan error, 1)
	go func() { rDone <- m.RLock(bg) }()
	waitFor(t, "reader queued behind writer", func() bool {
		_, _, q := m.state()
		return q == 2
	})

	m.RUnlock() // last active reader leaves
	if err := <-wDone; err != nil {
		t.Fatalf("writer Lock: %v", err)
	}
	// The writer holds the lock; the later reader is still queued.
	r, w, q := m.state()
	if !w || r != 0 || q != 1 {
		t.Fatalf("state = (%d, %v, %d), want (0, true, 1)", r, w, q)
	}
	m.Unlock()
	if err := <-rDone; err != nil {
		t.Fatalf("reader RLock: %v", err)
	}
	m.RUnlock()
}

// TestCancelWinsWhenProcessedFirst: the cancellation is processed
// before any grant is committed, so the waiter is dequeued and gets
// ctx.Err().
func TestCancelWinsWhenProcessedFirst(t *testing.T) {
	var m RWMutex
	mustLock(t, &m) // hold the write lock so the waiter queues

	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { done <- m.Lock(ctx) }()
	waitFor(t, "waiter queued", func() bool {
		_, _, q := m.state()
		return q == 1
	})

	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Lock returned %v, want context.Canceled", err)
	}
	waitFor(t, "waiter dequeued", func() bool {
		_, _, q := m.state()
		return q == 0
	})

	// The lock is still fully usable.
	m.Unlock()
	mustLock(t, &m)
	m.Unlock()
}

// TestGrantWinsWhenCommittedFirst: the grant is committed (by Unlock)
// before the cancellation is processed, so the waiter owns the lock
// even though its context is cancelled.
func TestGrantWinsWhenCommittedFirst(t *testing.T) {
	var m RWMutex
	mustLock(t, &m)

	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { done <- m.Lock(ctx) }()
	waitFor(t, "waiter queued", func() bool {
		_, _, q := m.state()
		return q == 1
	})

	m.Unlock() // commits the grant to the queued waiter
	waitFor(t, "grant committed", func() bool {
		_, w, q := m.state()
		return w && q == 0
	})
	cancel() // loses: the grant was already committed

	if err := <-done; err != nil {
		t.Fatalf("Lock returned %v, want nil (grant wins)", err)
	}
	m.Unlock() // the winner owns the lock and must release it
}

// TestCancelledReaderUnblocksFollowingReaders: removing a cancelled
// waiter from the queue re-runs the grant logic.
func TestCancelledWriterUnblocksReaders(t *testing.T) {
	var m RWMutex
	mustRLock(t, &m) // active reader

	ctx, cancel := context.WithCancel(bg)
	wDone := make(chan error, 1)
	go func() { wDone <- m.Lock(ctx) }()
	waitFor(t, "writer queued", func() bool {
		_, _, q := m.state()
		return q == 1
	})

	rDone := make(chan error, 1)
	go func() { rDone <- m.RLock(bg) }()
	waitFor(t, "reader queued behind writer", func() bool {
		_, _, q := m.state()
		return q == 2
	})

	cancel() // writer leaves the queue
	if err := <-wDone; err != context.Canceled {
		t.Fatalf("writer Lock: %v", err)
	}
	// The reader behind the cancelled writer is granted immediately,
	// joining the active reader.
	if err := <-rDone; err != nil {
		t.Fatalf("reader RLock: %v", err)
	}
	r, _, q := m.state()
	if r != 2 || q != 0 {
		t.Fatalf("state = (%d, _, %d), want (2, _, 0)", r, q)
	}
	m.RUnlock()
	m.RUnlock()
}

// TestDowngradeNoGap: downgrade converts the write hold to a read hold
// atomically; queued readers are granted in the same step and no
// writer can interpose.
func TestDowngradeNoGap(t *testing.T) {
	var m RWMutex
	mustLock(t, &m)

	rDone := make(chan error, 1)
	go func() { rDone <- m.RLock(bg) }()
	waitFor(t, "reader queued", func() bool {
		_, _, q := m.state()
		return q == 1
	})

	wDone := make(chan error, 1)
	go func() { wDone <- m.Lock(bg) }()
	waitFor(t, "writer queued behind reader", func() bool {
		_, _, q := m.state()
		return q == 2
	})

	m.Downgrade()

	// In the same atomic step: the downgraded holder became a reader
	// and the queued reader was granted; the writer is still queued.
	if err := <-rDone; err != nil {
		t.Fatalf("reader RLock: %v", err)
	}
	r, w, q := m.state()
	if r != 2 || w || q != 1 {
		t.Fatalf("state = (%d, %v, %d), want (2, false, 1)", r, w, q)
	}

	// The writer cannot proceed while the downgraded hold is active.
	m.RUnlock() // downgraded holder releases
	r, w, q = m.state()
	if r != 1 || w || q != 1 {
		t.Fatalf("state = (%d, %v, %d), want (1, false, 1)", r, w, q)
	}

	m.RUnlock() // last reader releases; writer is granted
	if err := <-wDone; err != nil {
		t.Fatalf("writer Lock: %v", err)
	}
	m.Unlock()
}

func TestDowngradePanicsWithoutWriteLock(t *testing.T) {
	var m RWMutex
	defer func() {
		if recover() == nil {
			t.Fatal("Downgrade without write lock did not panic")
		}
	}()
	m.Downgrade()
}

// TestStress hammers the lock with random operations while a checker
// verifies the exclusion invariants on every acquire/release. Run
// with -race.
func TestStress(t *testing.T) {
	var m RWMutex

	var cmu sync.Mutex
	writers, readers := 0, 0
	acquireW := func() {
		cmu.Lock()
		defer cmu.Unlock()
		if writers != 0 || readers != 0 {
			t.Errorf("write acquired while writers=%d readers=%d", writers, readers)
		}
		writers++
	}
	releaseW := func() {
		cmu.Lock()
		defer cmu.Unlock()
		writers--
		if writers != 0 {
			t.Errorf("write release left writers=%d", writers)
		}
	}
	acquireR := func() {
		cmu.Lock()
		defer cmu.Unlock()
		if writers != 0 {
			t.Errorf("read acquired while a writer is active")
		}
		readers++
	}
	releaseR := func() {
		cmu.Lock()
		defer cmu.Unlock()
		readers--
		if readers < 0 {
			t.Errorf("readers went negative")
		}
	}
	// downgrade mirrors the lock's atomic write->read conversion in the
	// checker's bookkeeping. It must be called while the caller still
	// holds the write lock (just before m.Downgrade): no other goroutine
	// can be granted in that window, so the two updates linearize.
	downgrade := func() {
		cmu.Lock()
		defer cmu.Unlock()
		writers--
		readers++
		if writers != 0 {
			t.Errorf("downgrade left writers=%d", writers)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for j := 0; j < 300; j++ {
				switch rng.Intn(6) {
				case 0, 1: // plain read
					if err := m.RLock(bg); err == nil {
						acquireR()
						runtime.Gosched()
						releaseR()
						m.RUnlock()
					}
				case 2: // plain write
					if err := m.Lock(bg); err == nil {
						acquireW()
						runtime.Gosched()
						releaseW()
						m.Unlock()
					}
				case 3: // write then downgrade
					if err := m.Lock(bg); err == nil {
						acquireW()
						downgrade()
						m.Downgrade()
						runtime.Gosched()
						releaseR()
						m.RUnlock()
					}
				case 4: // cancellable read
					ctx, cancel := context.WithCancel(bg)
					if rng.Intn(2) == 0 {
						cancel()
					} else {
						time.AfterFunc(time.Duration(rng.Intn(200))*time.Microsecond, cancel)
					}
					if err := m.RLock(ctx); err == nil {
						acquireR()
						runtime.Gosched()
						releaseR()
						m.RUnlock()
					}
				case 5: // cancellable write
					ctx, cancel := context.WithCancel(bg)
					if rng.Intn(2) == 0 {
						cancel()
					} else {
						time.AfterFunc(time.Duration(rng.Intn(200))*time.Microsecond, cancel)
					}
					if err := m.Lock(ctx); err == nil {
						acquireW()
						runtime.Gosched()
						releaseW()
						m.Unlock()
					}
				}
			}
		}(int64(i))
	}
	wg.Wait()

	r, w, q := m.state()
	if r != 0 || w || q != 0 {
		t.Fatalf("final state = (%d, %v, %d), want (0, false, 0)", r, w, q)
	}
}
