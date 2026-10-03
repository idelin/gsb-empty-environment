package arbiter

import (
	"errors"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMutualExclusionUnderContention(t *testing.T) {
	m := NewManager()
	const nodes = 16
	const iters = 200
	var counter int // plain int: the race detector validates exclusion
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			m.Begin(id)
			defer m.Finish(id)
			for i := 0; i < iters; {
				err := m.Lock(id, 1, Exclusive)
				if errors.Is(err, ErrAborted) {
					// Wounded by an older txn: roll back and retry
					// with a fresh timestamp.
					m.Finish(id)
					m.Begin(id)
					continue
				}
				if err != nil {
					t.Errorf("lock: %v", err)
					return
				}
				tmp := counter
				tmp++
				counter = tmp
				m.Unlock(id, 1)
				i++
			}
		}(uint64(n + 1))
	}
	wg.Wait()
	if counter != nodes*iters {
		t.Fatalf("counter = %d, want %d (mutual exclusion violated)", counter, nodes*iters)
	}
}

func TestSharedLocksAreConcurrent(t *testing.T) {
	m := NewManager()
	const readers = 4
	var inside int32
	var maxSeen int32
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		m.Begin(uint64(r + 1))
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			defer m.Finish(id)
			if err := m.Lock(id, 7, Shared); err != nil {
				t.Errorf("shared lock: %v", err)
				return
			}
			cur := atomic.AddInt32(&inside, 1)
			for {
				old := atomic.LoadInt32(&maxSeen)
				if cur <= old || atomic.CompareAndSwapInt32(&maxSeen, old, cur) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond) // overlap window
			atomic.AddInt32(&inside, -1)
			m.Unlock(id, 7)
		}(uint64(r + 1))
	}
	wg.Wait()
	if atomic.LoadInt32(&maxSeen) != readers {
		t.Fatalf("max concurrent shared holders = %d, want %d", maxSeen, readers)
	}
}

func TestExclusiveBlocksShared(t *testing.T) {
	m := NewManager()
	m.Begin(1)
	m.Begin(2)
	if err := m.Lock(1, 5, Exclusive); err != nil {
		t.Fatalf("lock: %v", err)
	}
	granted := make(chan error, 1)
	go func() { granted <- m.Lock(2, 5, Shared) }()
	select {
	case err := <-granted:
		t.Fatalf("shared lock granted while exclusive held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	m.Unlock(1, 5)
	if err := <-granted; err != nil {
		t.Fatalf("shared lock after release: %v", err)
	}
	m.Finish(1)
	m.Finish(2)
}

func TestOlderWoundsYoungerHolder(t *testing.T) {
	m := NewManager()
	m.Begin(1) // older
	m.Begin(2) // younger
	if err := m.Lock(2, 9, Exclusive); err != nil {
		t.Fatalf("younger lock: %v", err)
	}
	granted := make(chan error, 1)
	go func() { granted <- m.Lock(1, 9, Exclusive) }()
	// The older txn wounds the younger holder; the younger txn's
	// locks are released when it rolls back (Finish).
	waitFor(t, "younger txn to be marked aborted", func() bool { return m.IsAborted(2) })
	if err := m.Lock(2, 10, Exclusive); !errors.Is(err, ErrAborted) {
		t.Fatalf("wounded txn should see ErrAborted, got %v", err)
	}
	select {
	case err := <-granted:
		t.Fatalf("older txn must wait for victim rollback, got %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	m.Finish(2) // victim rolls back, releasing its locks
	if err := <-granted; err != nil {
		t.Fatalf("older lock: %v", err)
	}
	m.Finish(1)
}

func TestYoungerWaitsForOlder(t *testing.T) {
	m := NewManager()
	m.Begin(1) // older
	m.Begin(2) // younger
	if err := m.Lock(1, 3, Exclusive); err != nil {
		t.Fatalf("older lock: %v", err)
	}
	granted := make(chan error, 1)
	go func() { granted <- m.Lock(2, 3, Exclusive) }()
	select {
	case err := <-granted:
		t.Fatalf("younger txn should wait, got %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if m.IsAborted(2) {
		t.Fatalf("younger txn must wait, not be aborted")
	}
	m.Unlock(1, 3)
	if err := <-granted; err != nil {
		t.Fatalf("younger lock after older released: %v", err)
	}
	m.Finish(1)
	m.Finish(2)
}

func TestUpgrade(t *testing.T) {
	m := NewManager()
	m.Begin(1)
	if err := m.Lock(1, 4, Shared); err != nil {
		t.Fatalf("shared lock: %v", err)
	}
	if err := m.Lock(1, 4, Exclusive); err != nil {
		t.Fatalf("sole-holder upgrade should succeed: %v", err)
	}
	m.Finish(1)

	// Two holders both upgrading: the younger is wounded.
	m.Begin(2) // older
	m.Begin(3) // younger
	if err := m.Lock(2, 8, Shared); err != nil {
		t.Fatalf("t2 shared: %v", err)
	}
	if err := m.Lock(3, 8, Shared); err != nil {
		t.Fatalf("t3 shared: %v", err)
	}
	olderRes := make(chan error, 1)
	go func() { olderRes <- m.Lock(2, 8, Exclusive) }()
	waitFor(t, "younger upgrader aborted", func() bool { return m.IsAborted(3) })
	m.Finish(3) // victim rolls back, releasing its shared lock
	if err := <-olderRes; err != nil {
		t.Fatalf("older upgrade should win after wounding: %v", err)
	}
	m.Finish(2)
}

func TestExplicitAbortWakesWaiter(t *testing.T) {
	m := NewManager()
	m.Begin(1)
	m.Begin(2)
	if err := m.Lock(1, 6, Exclusive); err != nil {
		t.Fatalf("lock: %v", err)
	}
	waitRes := make(chan error, 1)
	go func() { waitRes <- m.Lock(2, 6, Exclusive) }()
	waitFor(t, "waiter queued", func() bool { return m.Waiters(6) == 1 })
	m.Abort(2)
	if err := <-waitRes; !errors.Is(err, ErrAborted) {
		t.Fatalf("aborted waiter should see ErrAborted, got %v", err)
	}
	m.Finish(1)
	m.Finish(2)
}

func TestUnknownTxn(t *testing.T) {
	m := NewManager()
	if err := m.Lock(999, 1, Exclusive); !errors.Is(err, ErrUnknownTxn) {
		t.Fatalf("expected ErrUnknownTxn, got %v", err)
	}
}

// TestNoDeadlockRandom hammers the manager with lock graphs that
// would deadlock under naive locking; wound-wait must keep the
// system live. Every transaction eventually commits (with retries).
func TestNoDeadlockRandom(t *testing.T) {
	m := NewManager()
	const nodes = 12
	const resources = 6
	const txnsPerNode = 30

	var committed int64
	var wg sync.WaitGroup
	wg.Add(nodes)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	for n := 0; n < nodes; n++ {
		go func(id uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for tx := 0; tx < txnsPerNode; tx++ {
				m.Begin(id) // re-Begin resets abort state and timestamp
				locks := 1 + rng.Intn(3)
				aborted := false
				var held []uint64
				for l := 0; l < locks; l++ {
					res := uint64(rng.Intn(resources))
					mode := Shared
					if rng.Intn(2) == 0 {
						mode = Exclusive
					}
					if err := m.Lock(id, res, mode); err != nil {
						if !errors.Is(err, ErrAborted) {
							t.Errorf("unexpected lock error: %v", err)
						}
						aborted = true
						break
					}
					held = append(held, res)
				}
				if !aborted {
					atomic.AddInt64(&committed, 1)
				}
				m.Finish(id)
			}
		}(uint64(n + 1))
	}

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("deadlock suspected: transactions did not finish")
	}
	if atomic.LoadInt64(&committed) == 0 {
		t.Fatalf("no transaction committed; system not live")
	}
	for r := 0; r < resources; r++ {
		if n := m.Waiters(uint64(r)); n != 0 {
			t.Fatalf("resource %d still has %d waiters after all txns finished", r, n)
		}
		if h := m.Holders(uint64(r)); len(h) != 0 {
			t.Fatalf("resource %d still held by %v after all txns finished", r, h)
		}
	}
}

// TestNoGoroutineLeak asserts the manager spawns no background
// goroutines and blocked waiters are always released.
func TestNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	m := NewManager()
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		m.Begin(uint64(n + 1))
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			defer m.Finish(uint64(id))
			for i := 0; i < 50; i++ {
				if err := m.Lock(id, uint64(i%3), Exclusive); err != nil {
					return // wounded; Finish still runs
				}
				time.Sleep(time.Microsecond)
			}
		}(uint64(n + 1))
	}
	wg.Wait()

	// Blocked waiters that get aborted must also release their goroutine.
	m2 := NewManager()
	m2.Begin(1)
	m2.Begin(2)
	if err := m2.Lock(1, 1, Exclusive); err != nil {
		t.Fatalf("lock: %v", err)
	}
	var wg2 sync.WaitGroup
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		m2.Lock(2, 1, Exclusive) // will be aborted
	}()
	waitFor(t, "waiter queued", func() bool { return m2.Waiters(1) == 1 })
	m2.Abort(2)
	wg2.Wait()
	m2.Finish(1)
	m2.Finish(2)

	time.Sleep(50 * time.Millisecond)
	if got := runtime.NumGoroutine(); got > baseline+1 {
		t.Fatalf("goroutine leak: baseline %d, now %d", baseline, got)
	}
}
