package ss2pl

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// runWithRetry executes fn as a transaction, rolling back and retrying when
// the deadlock detector picks this transaction as a victim.
func runWithRetry(db *DB, fn func(*Txn) error) (*Txn, error) {
	for attempts := 0; attempts < 1000; attempts++ {
		t := db.Begin()
		if err := t.Exec(fn); err != nil {
			continue // victim of deadlock or crash: already rolled back
		}
		return t, nil
	}
	return nil, fmt.Errorf("transaction could not commit after retries")
}

// TestConcurrentContention hammers shared keys from many goroutines; every
// increment must survive, which only holds if the schedule is serializable.
func TestConcurrentContention(t *testing.T) {
	db := NewDB()
	db.Set("counter", 0)

	const workers = 16
	const perWorker = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				_, err := runWithRetry(db, func(tx *Txn) error {
					v, err := tx.Read("counter")
					if err != nil {
						return err
					}
					return tx.Write("counter", v+1)
				})
				if err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got, _ := db.Get("counter")
	if want := workers * perWorker; got != want {
		t.Fatalf("lost update: counter = %d, want %d", got, want)
	}
}

// TestNoEarlyRelease uses the lock monitor to prove a transaction keeps its
// full row-lock set until it issues Commit, blocking a rival the whole time.
func TestNoEarlyRelease(t *testing.T) {
	db := NewDB()
	db.Set("k1", 1)
	db.Set("k2", 2)

	txnA := db.Begin()
	if _, err := txnA.Read("k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := txnA.Read("k2"); err != nil {
		t.Fatal(err)
	}

	blocked := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		txnB := db.Begin()
		close(blocked)
		// Blocks until txnA commits: k1 is locked by txnA.
		if err := txnB.Write("k1", 99); err != nil {
			t.Error(err)
			return
		}
		if err := txnB.Commit(); err != nil {
			t.Error(err)
		}
	}()

	<-blocked
	time.Sleep(50 * time.Millisecond) // let txnB queue up on k1

	// Monitor inspection before Commit: txnA must still hold BOTH row locks.
	lm := db.LockManager()
	held := lm.HeldBy(txnA.ID())
	if len(held) != 2 || held["k1"] != Shared || held["k2"] != Shared {
		t.Fatalf("lock released before commit! held = %v", held)
	}
	var waiters []uint64
	for _, info := range lm.Snapshot() {
		if info.Resource == "k1" {
			waiters = info.Waiters
		}
	}
	if len(waiters) != 1 {
		t.Fatalf("expected txnB queued on k1, waiters = %v", waiters)
	}

	if err := txnA.Commit(); err != nil {
		t.Fatal(err)
	}
	<-done

	if got, _ := db.Get("k1"); got != 99 {
		t.Fatalf("k1 = %d, want 99", got)
	}
	if locks := lm.HeldBy(txnA.ID()); len(locks) != 0 {
		t.Fatalf("locks leaked after commit: %v", locks)
	}
}

// TestShrinkPhaseEnforcement verifies no lock may be acquired once the
// transaction leaves the growing phase.
func TestShrinkPhaseEnforcement(t *testing.T) {
	db := NewDB()
	db.Set("k", 1)

	tx := db.Begin()
	if _, err := tx.Read("k"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Read("k"); err != ErrTxnNotGrowing {
		t.Fatalf("read after commit: err = %v, want ErrTxnNotGrowing", err)
	}
	if err := tx.Write("k", 2); err != ErrTxnNotGrowing {
		t.Fatalf("write after commit: err = %v, want ErrTxnNotGrowing", err)
	}
	if err := tx.Commit(); err != ErrTxnNotGrowing {
		t.Fatalf("double commit: err = %v, want ErrTxnNotGrowing", err)
	}
}

// TestCrashRollback injects a panic mid-transaction and checks that every
// lock is released and every write is undone.
func TestCrashRollback(t *testing.T) {
	db := NewDB()
	db.Set("balance", 100)

	tx := db.Begin()
	err := tx.Exec(func(t *Txn) error {
		if err := t.Write("balance", 50); err != nil {
			return err
		}
		if err := t.Write("audit", 1); err != nil {
			return err
		}
		panic("injected crash") // simulated failure before commit
	})
	if err == nil {
		t.Fatal("expected crash error, got nil")
	}

	if got, _ := db.Get("balance"); got != 100 {
		t.Fatalf("balance = %d, want 100 (rollback failed)", got)
	}
	if _, ok := db.Get("audit"); ok {
		t.Fatal("audit key should have been removed by rollback")
	}
	if locks := db.LockManager().HeldBy(tx.ID()); len(locks) != 0 {
		t.Fatalf("crashed txn still holds locks: %v", locks)
	}
	if phase := tx.Phase(); phase != Done {
		t.Fatalf("phase = %v, want done", phase)
	}

	// The lock manager must be usable afterwards.
	if _, err := runWithRetry(db, func(t2 *Txn) error {
		return t2.Write("balance", 200)
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Get("balance"); got != 200 {
		t.Fatalf("balance = %d, want 200", got)
	}
}

// TestExplicitRollback covers the non-crash abort path.
func TestExplicitRollback(t *testing.T) {
	db := NewDB()
	db.Set("k", 7)

	tx := db.Begin()
	if err := tx.Write("k", 8); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Get("k"); got != 7 {
		t.Fatalf("k = %d, want 7", got)
	}
	if locks := db.LockManager().HeldBy(tx.ID()); len(locks) != 0 {
		t.Fatalf("locks held after rollback: %v", locks)
	}
}

// TestDeadlockDetection creates a classic AB-BA deadlock; the detector must
// reject exactly one requester, and the survivor must be able to finish.
func TestDeadlockDetection(t *testing.T) {
	db := NewDB()
	db.Set("a", 1)
	db.Set("b", 1)

	txnA := db.Begin()
	txnB := db.Begin()
	if err := txnA.Write("a", 10); err != nil {
		t.Fatal(err)
	}
	if err := txnB.Write("b", 20); err != nil {
		t.Fatal(err)
	}

	aErr := make(chan error, 1)
	go func() { aErr <- txnA.Write("b", 11) }() // waits on txnB
	time.Sleep(50 * time.Millisecond)           // ensure A is queued first
	bErr := txnB.Write("a", 21)                 // would close the cycle

	if bErr != ErrDeadlock {
		t.Fatalf("txnB write err = %v, want ErrDeadlock", bErr)
	}
	if err := txnB.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-aErr; err != nil {
		t.Fatalf("txnA should proceed after victim rollback: %v", err)
	}
	if err := txnA.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Get("b"); got != 11 {
		t.Fatalf("b = %d, want 11", got)
	}
	if got, _ := db.Get("a"); got != 10 {
		t.Fatalf("a = %d, want 10 (txnB rolled back)", got)
	}
}

// TestCommitOrderEquivalence checks the SS2PL guarantee: the final state
// equals the serial execution of transactions in commit-timestamp order.
func TestCommitOrderEquivalence(t *testing.T) {
	db := NewDB()
	db.Set("acc", 1)

	const n = 12
	type result struct {
		id       uint64
		commitTS uint64
	}
	results := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := runWithRetry(db, func(t2 *Txn) error {
				v, err := t2.Read("acc")
				if err != nil {
					return err
				}
				// f(v) = v*2 + id: order-dependent, so the final value
				// uniquely reflects the serialization order.
				return t2.Write("acc", v*2+int(t2.ID()))
			})
			if err != nil {
				t.Error(err)
				return
			}
			results <- result{id: tx.ID(), commitTS: tx.CommitTS()}
		}()
	}
	wg.Wait()
	close(results)

	var order []result
	for r := range results {
		order = append(order, r)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].commitTS < order[j].commitTS })

	want := 1
	for _, r := range order {
		want = want*2 + int(r.id)
	}
	if got, _ := db.Get("acc"); got != want {
		t.Fatalf("acc = %d, want %d from commit-order serial execution", got, want)
	}
}

// ExampleTxn is a runnable demo of the SS2PL kernel.
func ExampleTxn() {
	db := NewDB()
	db.Set("alice", 100)
	db.Set("bob", 50)

	// Transfer 30 from alice to bob.
	_ = db.Begin().Exec(func(tx *Txn) error {
		a, _ := tx.Read("alice")
		b, _ := tx.Read("bob")
		if err := tx.Write("alice", a-30); err != nil {
			return err
		}
		return tx.Write("bob", b+30)
	})

	// A crashing transaction is rolled back automatically.
	crashed := db.Begin()
	err := crashed.Exec(func(tx *Txn) error {
		_ = tx.Write("alice", 0)
		panic("power failure")
	})
	fmt.Println("crash handled:", err != nil)

	alice, _ := db.Get("alice")
	bob, _ := db.Get("bob")
	fmt.Printf("alice=%d bob=%d\n", alice, bob)
	fmt.Println("locks after crash:", len(db.LockManager().HeldBy(crashed.ID())))
	// Output:
	// crash handled: true
	// alice=70 bob=80
	// locks after crash: 0
}
