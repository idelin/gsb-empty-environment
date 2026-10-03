package arbiter

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestConcurrentProposeLinearizable hammers one key from many nodes and
// verifies the linearizability invariant: accepted versions form the exact
// gap-free sequence 1..N, each won by exactly one node.
func TestConcurrentProposeLinearizable(t *testing.T) {
	const nodes = 32
	const rounds = 500
	a := New()

	var mu sync.Mutex
	accepted := make(map[uint64]int) // version -> nodeID

	var wg sync.WaitGroup
	for id := 0; id < nodes; id++ {
		wg.Add(1)
		go func(nodeID int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				ver, val, _ := a.Read("hot")
				_ = val
				if newVer, ok := a.Propose("hot", nodeID, ver, nodeID); ok {
					mu.Lock()
					accepted[newVer] = nodeID
					mu.Unlock()
				}
			}
		}(id)
	}
	wg.Wait()

	finalVer, _, _ := a.Read("hot")
	if uint64(len(accepted)) != finalVer {
		t.Fatalf("accepted %d distinct versions but final version is %d", len(accepted), finalVer)
	}
	for v := uint64(1); v <= finalVer; v++ {
		if _, ok := accepted[v]; !ok {
			t.Fatalf("version %d missing: accepted sequence has a gap", v)
		}
	}
	acc, rej := a.Stats()
	if acc != finalVer {
		t.Errorf("stats: accepted=%d, want %d", acc, finalVer)
	}
	if rej == 0 {
		t.Error("expected rejections under contention, got 0")
	}
	t.Logf("linearized %d accepted writes, %d rejected conflicts", acc, rej)
}

// TestTransactAtomicity runs concurrent "bank transfers" between accounts and
// asserts the conservation invariant: the total balance never changes and no
// account ever goes negative (all-or-nothing semantics).
func TestTransactAtomicity(t *testing.T) {
	const accounts = 16
	const perAccount = 1000
	const workers = 24
	const transfers = 400

	a := New()
	keys := make([]string, accounts)
	for i := range keys {
		keys[i] = fmt.Sprintf("acct-%02d", i)
		if _, ok := a.Propose(keys[i], 0, 0, perAccount); !ok {
			t.Fatal("seed failed")
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < transfers; i++ {
				from := (seed + i) % accounts
				to := (seed + i + 1) % accounts
				// Read-modify-write with bounded conflict retries.
				for attempt := 0; attempt < 64; attempt++ {
					vFrom, balFrom, _ := a.Read(keys[from])
					vTo, balTo, _ := a.Read(keys[to])
					if balFrom.(int) <= 0 {
						break // nothing to transfer; not a conflict
					}
					err := a.Transact(seed,
						Op{Key: keys[from], Expect: vFrom, Value: balFrom.(int) - 1},
						Op{Key: keys[to], Expect: vTo, Value: balTo.(int) + 1},
					)
					if err == nil {
						break
					}
					if !errors.Is(err, ErrConflict) {
						t.Errorf("unexpected error: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	total := 0
	for _, k := range keys {
		_, bal, _ := a.Read(k)
		if bal.(int) < 0 {
			t.Errorf("account %s went negative: %d", k, bal)
		}
		total += bal.(int)
	}
	if total != accounts*perAccount {
		t.Fatalf("conservation violated: total=%d, want %d", total, accounts*perAccount)
	}
}

// TestTransactDeadlockFree runs transactions whose key sets overlap in
// opposite orders; without deterministic lock ordering this would deadlock.
func TestTransactDeadlockFree(t *testing.T) {
	a := New()
	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		a.Propose(k, 0, 0, 0)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for w := 0; w < 16; w++ {
			wg.Add(1)
			go func(seed int) {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					// Alternate forward and reverse key windows.
					window := []string{keys[i%5], keys[(i+1)%5], keys[(i+2)%5]}
					if seed%2 == 1 {
						window[0], window[2] = window[2], window[0]
					}
					ops := make([]Op, len(window))
					ok := true
					for j, k := range window {
						v, val, _ := a.Read(k)
						if val.(int) >= 1<<30 {
							ok = false
							break
						}
						ops[j] = Op{Key: k, Expect: v, Value: val.(int) + 1}
					}
					if ok {
						_ = a.Transact(seed, ops...) // conflicts are fine
					}
				}
			}(w)
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: transactions did not complete within 15s")
	}
}

// TestTransactAtomicRollback verifies that a transaction failing validation
// leaves every slot untouched.
func TestTransactAtomicRollback(t *testing.T) {
	a := New()
	a.Propose("x", 0, 0, 10)
	a.Propose("y", 0, 0, 20)

	err := a.Transact(1,
		Op{Key: "x", Expect: 1, Value: 11},
		Op{Key: "y", Expect: 999, Value: 21}, // stale expectation
	)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if ce.Key != "y" || ce.Current != 1 {
		t.Errorf("conflict detail: %+v", ce)
	}
	if _, v, _ := a.Read("x"); v != 10 {
		t.Errorf("x mutated despite rollback: %v", v)
	}
	if _, v, _ := a.Read("y"); v != 20 {
		t.Errorf("y mutated despite rollback: %v", v)
	}
}

func TestTransactRejectsDuplicateKeys(t *testing.T) {
	a := New()
	if err := a.Transact(1, Op{Key: "k"}, Op{Key: "k"}); err == nil {
		t.Error("duplicate keys in one transaction should be rejected")
	}
}

// TestVersionMonotonicPerKey: a single slot's version never goes backwards
// and the writer recorded matches the winning proposal.
func TestVersionMonotonicPerKey(t *testing.T) {
	a := New()
	for i := uint64(0); i < 100; i++ {
		if _, ok := a.Propose("k", 7, i, i); !ok {
			t.Fatalf("proposal %d rejected without contention", i)
		}
	}
	ver, val, ok := a.Read("k")
	if !ok || ver != 100 || val.(uint64) != 99 {
		t.Errorf("final state: ver=%d val=%v ok=%v", ver, val, ok)
	}
	// Stale expectation must now fail.
	if _, ok := a.Propose("k", 7, 50, 0); ok {
		t.Error("stale proposal accepted")
	}
}

func TestReadMissingKey(t *testing.T) {
	a := New()
	if _, _, ok := a.Read("nope"); ok {
		t.Error("missing key reported as present")
	}
}

// TestNoGoroutineLeak asserts the arbiter spawns no background goroutines
// and that the test harness itself leaves nothing running.
func TestNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	a := New()
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				v, _, _ := a.Read("k")
				a.Propose("k", id, v, id)
			}
		}(w)
	}
	wg.Wait()

	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutine leak: baseline=%d, now=%d", baseline, runtime.NumGoroutine())
}
