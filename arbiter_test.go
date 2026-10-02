package aries

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// acquireAsync runs Acquire in a goroutine and reports the outcome.
func acquireAsync(lm *LockManager, txnID int64, key string, mode LockMode) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- lm.Acquire(txnID, key, mode) }()
	return ch
}

// waitQueued blocks until key has exactly n queued waiters.
func waitQueued(t *testing.T, lm *LockManager, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for lm.waiters(key) != n {
		if time.Now().After(deadline) {
			t.Fatalf("key %q: waiters never reached %d", key, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func mustAcquire(t *testing.T, lm *LockManager, txnID int64, key string, mode LockMode) {
	t.Helper()
	if err := lm.Acquire(txnID, key, mode); err != nil {
		t.Fatalf("Acquire(txn=%d, %q, %v): %v", txnID, key, mode, err)
	}
}

// requireBlocked fails if ch delivers within the window.
func requireBlocked(t *testing.T, ch <-chan error, what string) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("%s: expected to block, got %v", what, err)
	case <-time.After(50 * time.Millisecond):
	}
}

func requireGranted(t *testing.T, ch <-chan error, what string) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("%s: expected grant, got %v", what, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: timed out waiting for grant", what)
	}
}

func TestSharedLocksAreCompatible(t *testing.T) {
	lm := NewLockManager()
	mustAcquire(t, lm, 1, "k", Shared)
	mustAcquire(t, lm, 2, "k", Shared) // co-granted immediately
	lm.ReleaseAll(1)
	lm.ReleaseAll(2)
	assertDrained(t, lm)
}

func TestExclusiveBlocksSharedAndExclusive(t *testing.T) {
	lm := NewLockManager()
	mustAcquire(t, lm, 1, "k", Exclusive)
	blockedS := acquireAsync(lm, 2, "k", Shared)
	waitQueued(t, lm, "k", 1)
	blockedX := acquireAsync(lm, 3, "k", Exclusive)
	waitQueued(t, lm, "k", 2)
	requireBlocked(t, blockedS, "S behind X")
	requireBlocked(t, blockedX, "X behind X")
	lm.ReleaseAll(1)
	// FIFO: txn 2 queued first, so it is served before txn 3.
	requireGranted(t, blockedS, "S after release")
	requireBlocked(t, blockedX, "X still behind S holder")
	lm.ReleaseAll(2)
	requireGranted(t, blockedX, "X after S release")
	lm.ReleaseAll(3)
	assertDrained(t, lm)
}

func TestFIFOFairness(t *testing.T) {
	lm := NewLockManager()
	mustAcquire(t, lm, 1, "k", Exclusive)
	first := acquireAsync(lm, 2, "k", Exclusive)
	waitQueued(t, lm, "k", 1)
	second := acquireAsync(lm, 3, "k", Shared)
	waitQueued(t, lm, "k", 2)
	requireBlocked(t, first, "T2 queued")
	requireBlocked(t, second, "T3 queued behind T2 even though S")
	lm.ReleaseAll(1)
	requireGranted(t, first, "T2 served first")
	requireBlocked(t, second, "T3 waits for T2")
	lm.ReleaseAll(2)
	requireGranted(t, second, "T3 served after T2")
	lm.ReleaseAll(3)
	assertDrained(t, lm)
}

func TestSharedWaitersAreCoGranted(t *testing.T) {
	lm := NewLockManager()
	mustAcquire(t, lm, 1, "k", Exclusive)
	s1 := acquireAsync(lm, 2, "k", Shared)
	waitQueued(t, lm, "k", 1)
	s2 := acquireAsync(lm, 3, "k", Shared)
	waitQueued(t, lm, "k", 2)
	requireBlocked(t, s1, "S1 queued")
	requireBlocked(t, s2, "S2 queued")
	lm.ReleaseAll(1)
	requireGranted(t, s1, "S1 co-granted")
	requireGranted(t, s2, "S2 co-granted")
	lm.ReleaseAll(2)
	lm.ReleaseAll(3)
	assertDrained(t, lm)
}

// TestWoundWaitOlderWoundsYounger: an older requester aborts a conflicting
// younger holder; the victim observes ErrAborted and the older txn proceeds.
func TestWoundWaitOlderWoundsYounger(t *testing.T) {
	lm := NewLockManager()
	const older, younger = 1, 2
	mustAcquire(t, lm, younger, "k", Exclusive)
	olderReq := acquireAsync(lm, older, "k", Exclusive)
	requireBlocked(t, olderReq, "older waits while younger holds")

	// The younger txn is wounded: its next acquire fails.
	if err := lm.Acquire(younger, "other", Shared); !errors.Is(err, ErrAborted) {
		t.Fatalf("wounded txn next acquire = %v, want ErrAborted", err)
	}
	lm.ReleaseAll(younger) // victim rolls back and releases
	requireGranted(t, olderReq, "older granted after victim release")
	lm.ReleaseAll(older)
	if _, wounds := lm.Stats(); wounds != 1 {
		t.Fatalf("wounds = %d, want 1", wounds)
	}
	assertDrained(t, lm)
}

// TestWoundWaitYoungerWaitsForOlder: a younger requester must never abort an
// older holder; it waits until the older txn releases.
func TestWoundWaitYoungerWaitsForOlder(t *testing.T) {
	lm := NewLockManager()
	const older, younger = 1, 2
	mustAcquire(t, lm, older, "k", Exclusive)
	youngerReq := acquireAsync(lm, younger, "k", Exclusive)
	requireBlocked(t, youngerReq, "younger waits for older")
	if _, wounds := lm.Stats(); wounds != 0 {
		t.Fatalf("younger must not wound older; wounds = %d", wounds)
	}
	lm.ReleaseAll(older)
	requireGranted(t, youngerReq, "younger granted after older release")
	lm.ReleaseAll(younger)
	assertDrained(t, lm)
}

// TestWoundedWaiterIsWoken: a younger *waiting* txn is wounded in place and
// its Acquire returns ErrAborted instead of hanging.
func TestWoundedWaiterIsWoken(t *testing.T) {
	lm := NewLockManager()
	const older, younger = 1, 2
	mustAcquire(t, lm, 3, "k", Exclusive) // unrelated oldest holder
	youngerReq := acquireAsync(lm, younger, "k", Exclusive)
	waitQueued(t, lm, "k", 1)
	requireBlocked(t, youngerReq, "younger queued")
	olderReq := acquireAsync(lm, older, "k", Exclusive)
	// The older request wounds the younger waiter immediately.
	select {
	case err := <-youngerReq:
		if !errors.Is(err, ErrAborted) {
			t.Fatalf("wounded waiter got %v, want ErrAborted", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wounded waiter was not woken")
	}
	requireBlocked(t, olderReq, "older still waits for holder 3")
	lm.ReleaseAll(3)
	requireGranted(t, olderReq, "older granted")
	lm.ReleaseAll(older)
	assertDrained(t, lm)
}

func TestReentrantAndDowngradeFree(t *testing.T) {
	lm := NewLockManager()
	mustAcquire(t, lm, 1, "k", Shared)
	mustAcquire(t, lm, 1, "k", Shared)    // re-entrant S
	mustAcquire(t, lm, 1, "k", Exclusive) // upgrade, sole holder
	mustAcquire(t, lm, 1, "k", Shared)    // already X: no-op
	lm.ReleaseAll(1)
	assertDrained(t, lm)
}

// TestUpgradeConflict: two shared holders both upgrade; wound-wait must
// resolve it without deadlock — the younger is aborted.
func TestUpgradeConflict(t *testing.T) {
	lm := NewLockManager()
	const older, younger = 1, 2
	mustAcquire(t, lm, older, "k", Shared)
	mustAcquire(t, lm, younger, "k", Shared)
	youngerUpgrade := acquireAsync(lm, younger, "k", Exclusive)
	waitQueued(t, lm, "k", 1)
	requireBlocked(t, youngerUpgrade, "younger upgrade waits for older holder")
	olderUpgrade := acquireAsync(lm, older, "k", Exclusive)
	// Older upgrader wounds the younger co-holder.
	select {
	case err := <-youngerUpgrade:
		if !errors.Is(err, ErrAborted) {
			t.Fatalf("younger upgrade = %v, want ErrAborted", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upgrade deadlock: younger never wounded")
	}
	lm.ReleaseAll(younger)
	requireGranted(t, olderUpgrade, "older upgrade granted")
	lm.ReleaseAll(older)
	assertDrained(t, lm)
}

func TestAbortReleasesAndWakes(t *testing.T) {
	lm := NewLockManager()
	mustAcquire(t, lm, 1, "k", Exclusive)
	waiter := acquireAsync(lm, 2, "k", Exclusive)
	waitQueued(t, lm, "k", 1)
	requireBlocked(t, waiter, "txn2 queued")
	lm.Abort(1)
	requireGranted(t, waiter, "txn2 granted after abort of holder")
	lm.ReleaseAll(2)
	assertDrained(t, lm)
}

func assertDrained(t *testing.T, lm *LockManager) {
	t.Helper()
	entries, txns := lm.Pending()
	if entries != 0 || txns != 0 {
		t.Fatalf("lock manager leaked state: %d entries, %d txns", entries, txns)
	}
}

// TestConcurrentArbiter hammers the arbiter from many executor goroutines
// ("nodes") performing account transfers under exclusive locks. It asserts:
//   - the conservation invariant (sum of balances) — i.e. mutual exclusion
//     and linearizable grant order were never violated;
//   - no deadlock (watchdog timeout);
//   - no goroutine or lock-table leaks after the run.
func TestConcurrentArbiter(t *testing.T) {
	const (
		nodes       = 8
		txnsPerNode = 200
		accounts    = 12
		initial     = 1000
	)
	lm := NewLockManager()
	balances := make([]int64, accounts)
	for i := range balances {
		balances[i] = initial
	}
	var txnSeq atomic.Int64
	var committed, retried atomic.Int64

	work := func() {
		var wg sync.WaitGroup
		for n := 0; n < nodes; n++ {
			wg.Add(1)
			go func(node int) {
				defer wg.Done()
				// Deterministic per-node PRNG keeps the run reproducible.
				seed := uint64(node)*2654435761 + 1
				rnd := func(m int64) int64 {
					seed = seed*6364136223846793005 + 1442695040888963407
					return int64((seed >> 33) % uint64(m))
				}
				for i := 0; i < txnsPerNode; i++ {
					txnID := txnSeq.Add(1)
					from, to := rnd(accounts), rnd(accounts)
					if from == to {
						continue
					}
					amount := 1 + rnd(50)
					err := func() error {
						// Lock in key order to exercise the arbiter's own
						// deadlock-freedom on top of wound-wait.
						k1, k2 := pageKey(from), pageKey(to)
						if k2 < k1 {
							k1, k2 = k2, k1
						}
						if err := lm.Acquire(txnID, k1, Exclusive); err != nil {
							return err
						}
						if err := lm.Acquire(txnID, k2, Exclusive); err != nil {
							return err
						}
						balances[from] -= amount
						balances[to] += amount
						return nil
					}()
					if err != nil {
						lm.ReleaseAll(txnID) // wounded: roll back, retry
						retried.Add(1)
						i--
						continue
					}
					lm.ReleaseAll(txnID)
					committed.Add(1)
				}
			}(n)
		}
		wg.Wait()
	}

	done := make(chan struct{})
	go func() { work(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock suspected: concurrent workload did not finish in 30s")
	}

	var sum int64
	for _, b := range balances {
		sum += b
	}
	if sum != accounts*initial {
		t.Fatalf("invariant violated: sum=%d, want %d", sum, accounts*initial)
	}
	if committed.Load() == 0 {
		t.Fatal("no transaction committed")
	}
	t.Logf("committed=%d retried(wounded)=%d", committed.Load(), retried.Load())
	assertDrained(t, lm)
}

// TestNoGoroutineLeak runs a mixed workload and asserts the goroutine count
// returns to baseline — the arbiter must not strand blocked acquirers.
func TestNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	lm := NewLockManager()
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				txnID := int64(node*1000 + i + 1)
				key := pageKey(int64(i % 4))
				if err := lm.Acquire(txnID, key, Exclusive); err != nil {
					lm.ReleaseAll(txnID)
					continue
				}
				lm.ReleaseAll(txnID)
			}
		}(n)
	}
	wg.Wait()
	assertDrained(t, lm)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline=%d now=%d", baseline, runtime.NumGoroutine())
}
