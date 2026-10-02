package ss2pl

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func newEnv() (*TxnManager, *Monitor) {
	store := NewStore()
	lm := NewLockManager()
	tm := NewTxnManager(store, lm)
	return tm, NewMonitor(tm, lm)
}

// TestNoLockReleasedBeforeCommit is the core acceptance check: while a
// transaction is still in its growing phase (no Commit issued), a
// conflicting transaction must remain blocked, and the monitor must not
// observe a single lock release.
func TestNoLockReleasedBeforeCommit(t *testing.T) {
	tm, mon := newEnv()
	tm.Store().Set("x", 1)

	t1 := tm.Begin()
	if err := t1.Write("x", 100); err != nil {
		t.Fatalf("t1 write: %v", err)
	}

	// t2 wants to read x; it must block until t1 commits.
	done := make(chan int64, 1)
	errCh := make(chan error, 1)
	go func() {
		t2 := tm.Begin()
		v, err := t2.Read("x")
		if err != nil {
			errCh <- err
			return
		}
		if err := t2.Commit(); err != nil {
			errCh <- err
			return
		}
		done <- v
	}()

	// Give t2 plenty of time to (illegally) proceed.
	select {
	case <-done:
		t.Fatal("t2 read x before t1 committed: lock released too early")
	case err := <-errCh:
		t.Fatalf("t2 failed: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	// t1 is still active and must still hold the X lock.
	held := tm.LockManager().HeldLocks(t1.ID())
	if len(held) != 1 || held[0] != "x" {
		t.Fatalf("t1 should still hold its lock, held=%v", held)
	}
	if v := mon.Violations(); len(v) > 0 {
		t.Fatalf("monitor violations before commit: %v", v)
	}

	if err := t1.Commit(); err != nil {
		t.Fatalf("t1 commit: %v", err)
	}

	select {
	case v := <-done:
		if v != 100 {
			t.Fatalf("t2 read %d, want committed value 100", v)
		}
	case err := <-errCh:
		t.Fatalf("t2 failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("t2 still blocked after t1 committed")
	}

	if v := mon.Violations(); len(v) > 0 {
		t.Fatalf("monitor violations: %v", v)
	}
}

// TestConcurrentContention hammers multiple shared resources with many
// concurrent transfer transactions and checks (a) the money invariant,
// (b) equivalence to the commit-time timestamp order, and (c) zero
// early-release violations.
func TestConcurrentContention(t *testing.T) {
	tm, mon := newEnv()
	accounts := []string{"a", "b", "c", "d", "e"}
	const initial = int64(1000)
	for _, a := range accounts {
		tm.Store().Set(a, initial)
	}

	const workers = 16
	const rounds = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				from := accounts[(w+r)%len(accounts)]
				to := accounts[(w+r+1)%len(accounts)]
				amount := int64((w*7+r*13)%50 + 1)
				// Retry loop: a deadlock victim aborts and retries.
				for {
					txn := tm.Begin()
					bal, err := txn.Read(from)
					if err == nil {
						err = txn.Write(from, bal-amount)
					}
					if err == nil {
						var bal2 int64
						bal2, err = txn.Read(to)
						if err == nil {
							err = txn.Write(to, bal2+amount)
						}
					}
					if err == ErrDeadlock || err == ErrWaitCancelled {
						continue // already aborted; retry with a fresh txn
					}
					if err != nil {
						t.Errorf("txn failed: %v", err)
						return
					}
					if err := txn.Commit(); err != nil {
						t.Errorf("commit: %v", err)
					}
					break
				}
			}
		}(w)
	}
	wg.Wait()

	// (a) Conservation of money across all committed transfers.
	snap := tm.Store().Snapshot()
	var total int64
	for _, a := range accounts {
		total += snap[a]
	}
	if want := initial * int64(len(accounts)); total != want {
		t.Fatalf("total balance = %d, want %d (lost update?)", total, want)
	}

	// (b) Schedule must be equivalent to commit-time timestamp order.
	if err := tm.VerifyCommitOrderEquivalence(); err != nil {
		t.Fatal(err)
	}

	// (c) No lock may have been released before its txn committed/aborted.
	if v := mon.Violations(); len(v) > 0 {
		t.Fatalf("monitor violations: %v", v)
	}

	if got := len(tm.CommitLog()); got != workers*rounds {
		t.Fatalf("commit log has %d entries, want %d", got, workers*rounds)
	}
}

// TestCrashRollback injects a crash into a transaction that holds locks
// and has dirty writes, then verifies recovery: modifications are undone,
// all locks are released, and other transactions proceed immediately.
func TestCrashRollback(t *testing.T) {
	tm, mon := newEnv()
	tm.Store().Set("x", 1)
	tm.Store().Set("y", 2)

	victim := tm.Begin()
	if err := victim.Write("x", 111); err != nil {
		t.Fatalf("write x: %v", err)
	}
	if err := victim.Write("y", 222); err != nil {
		t.Fatalf("write y: %v", err)
	}
	// A new key created by the doomed transaction must vanish on rollback.
	if err := victim.Write("tmp", 999); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	// Sanity: victim holds all three locks while active.
	if held := tm.LockManager().HeldLocks(victim.ID()); len(held) != 3 {
		t.Fatalf("victim holds %v, want 3 locks", held)
	}

	// Inject the crash: recovery must undo everything and free all locks.
	if err := tm.Crash(victim.ID()); err != nil {
		t.Fatalf("crash: %v", err)
	}

	if got, _ := tm.Store().Get("x"); got != 1 {
		t.Fatalf("x = %d after crash, want rolled-back 1", got)
	}
	if got, _ := tm.Store().Get("y"); got != 2 {
		t.Fatalf("y = %d after crash, want rolled-back 2", got)
	}
	if _, ok := tm.Store().Get("tmp"); ok {
		t.Fatal("tmp should have been removed by rollback")
	}
	if held := tm.LockManager().HeldLocks(victim.ID()); len(held) != 0 {
		t.Fatalf("victim still holds locks after crash: %v", held)
	}
	if state := victim.State(); state != TxnAborted {
		t.Fatalf("victim state = %v, want ABORTED", state)
	}

	// Another transaction must be able to grab the same rows at once.
	t2 := tm.Begin()
	done := make(chan error, 1)
	go func() {
		if err := t2.Write("x", 10); err != nil {
			done <- err
			return
		}
		if err := t2.Write("y", 20); err != nil {
			done <- err
			return
		}
		done <- t2.Commit()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("t2 after crash: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("t2 blocked: crashed transaction did not release its locks")
	}

	if v := mon.Violations(); len(v) > 0 {
		t.Fatalf("monitor violations: %v", v)
	}
}

// TestCrashWhileWaiting injects a crash into a transaction that is blocked
// on a lock wait; the wait must be cancelled and the txn cleaned up.
func TestCrashWhileWaiting(t *testing.T) {
	tm, _ := newEnv()
	tm.Store().Set("x", 1)

	holder := tm.Begin()
	if err := holder.Write("x", 42); err != nil {
		t.Fatalf("holder write: %v", err)
	}

	waiter := tm.Begin()
	waitErr := make(chan error, 1)
	go func() {
		_, err := waiter.Read("x") // blocks behind holder's X lock
		waitErr <- err
	}()
	time.Sleep(100 * time.Millisecond) // let the waiter actually queue up

	if err := tm.Crash(waiter.ID()); err != nil {
		t.Fatalf("crash waiter: %v", err)
	}
	select {
	case err := <-waitErr:
		if err != ErrWaitCancelled && err != ErrTxnNotActive {
			t.Fatalf("waiter got %v, want wait cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("crashed waiter never woke up")
	}
	if state := waiter.State(); state != TxnAborted {
		t.Fatalf("waiter state = %v, want ABORTED", state)
	}
	if err := holder.Commit(); err != nil {
		t.Fatalf("holder commit: %v", err)
	}
}

// TestDeadlockDetection forces a classic AB-BA deadlock; exactly one
// transaction must be chosen as victim, roll back, and let the other
// commit.
func TestDeadlockDetection(t *testing.T) {
	tm, mon := newEnv()
	tm.Store().Set("a", 0)
	tm.Store().Set("b", 0)

	t1 := tm.Begin()
	t2 := tm.Begin()

	if err := t1.Write("a", 1); err != nil {
		t.Fatalf("t1 write a: %v", err)
	}
	if err := t2.Write("b", 2); err != nil {
		t.Fatalf("t2 write b: %v", err)
	}

	// Both transactions now request the other's row; exactly one of them
	// closes the wait-for cycle and must be chosen as the victim.
	t1Err := make(chan error, 1)
	t2Err := make(chan error, 1)
	go func() { t1Err <- t1.Write("b", 1) }()
	go func() { t2Err <- t2.Write("a", 2) }()

	err1, err2 := <-t1Err, <-t2Err
	victims := 0
	if err1 == ErrDeadlock {
		victims++
	}
	if err2 == ErrDeadlock {
		victims++
	}
	if victims != 1 {
		t.Fatalf("got %d deadlock victims (t1=%v, t2=%v), want exactly 1", victims, err1, err2)
	}

	// The victim was aborted and rolled back; the survivor finishes.
	var survivor, victim *Txn
	if err1 == ErrDeadlock {
		survivor, victim = t2, t1
	} else {
		survivor, victim = t1, t2
	}
	if state := victim.State(); state != TxnAborted {
		t.Fatalf("victim state = %v, want ABORTED", state)
	}
	if held := tm.LockManager().HeldLocks(victim.ID()); len(held) != 0 {
		t.Fatalf("victim still holds locks: %v", held)
	}
	if err := survivor.Commit(); err != nil {
		t.Fatalf("survivor commit: %v", err)
	}

	// Only the survivor's writes are visible.
	wantA, wantB := int64(1), int64(1)
	if survivor == t2 {
		wantA, wantB = 2, 2
	}
	if got, _ := tm.Store().Get("a"); got != wantA {
		t.Fatalf("a = %d, want %d (survivor txn %d)", got, wantA, survivor.ID())
	}
	if got, _ := tm.Store().Get("b"); got != wantB {
		t.Fatalf("b = %d, want %d (survivor txn %d)", got, wantB, survivor.ID())
	}
	if v := mon.Violations(); len(v) > 0 {
		t.Fatalf("monitor violations: %v", v)
	}
}

// TestSharedLocksAreCompatible verifies readers do not block each other.
func TestSharedLocksAreCompatible(t *testing.T) {
	tm, _ := newEnv()
	tm.Store().Set("x", 7)

	const readers = 8
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			txn := tm.Begin()
			v, err := txn.Read("x")
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			if v != 7 {
				t.Errorf("read %d, want 7", v)
			}
			if err := txn.Commit(); err != nil {
				t.Errorf("commit: %v", err)
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent readers blocked each other")
	}
}

// TestDoubleCommitRejected ensures lifecycle transitions happen once.
func TestDoubleCommitRejected(t *testing.T) {
	tm, _ := newEnv()
	txn := tm.Begin()
	if err := txn.Write("k", 1); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := txn.Commit(); err != ErrTxnNotActive {
		t.Fatalf("second commit = %v, want ErrTxnNotActive", err)
	}
	if _, err := txn.Read("k"); err != ErrTxnNotActive {
		t.Fatalf("read after commit = %v, want ErrTxnNotActive", err)
	}
}

// Example-style sanity check of the lock table snapshot, handy when
// debugging the demo output.
func TestLockTableSnapshot(t *testing.T) {
	tm, _ := newEnv()
	txn := tm.Begin()
	if err := txn.Write("res1", 5); err != nil {
		t.Fatal(err)
	}
	snap := tm.LockManager().TableSnapshot()
	holders, ok := snap["res1"]
	if !ok || len(holders) != 1 || holders[txn.ID()] != XLock {
		t.Fatalf("snapshot = %v, want res1 X-locked by txn %d", snap, txn.ID())
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if snap := tm.LockManager().TableSnapshot(); len(snap) != 0 {
		t.Fatalf("snapshot after commit = %v, want empty", snap)
	}
	fmt.Println("snapshot test ok")
}
