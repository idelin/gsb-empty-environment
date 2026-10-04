package leasearb

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestArbiter(ttl time.Duration) (*Arbiter, *ManualClock) {
	clock := NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	return NewArbiter(clock, ttl), clock
}

func TestDuplicateVsStale(t *testing.T) {
	a, _ := newTestArbiter(10 * time.Second)
	gen, _ := a.Acquire("r", "alice")
	if got := a.Write("r", "alice", gen, "req-1", "x"); got.Status != StatusApplied {
		t.Fatalf("first write: %v", got)
	}
	a.Acquire("r", "bob") // handoff, gen bumped

	dup := a.Write("r", "alice", gen, "req-1", "x")
	if dup.Status != StatusDuplicate {
		t.Fatalf("retry of applied write = %v, want DUPLICATE", dup.Status)
	}
	stale := a.Write("r", "alice", gen, "req-2", "y")
	if stale.Status != StatusStale {
		t.Fatalf("new write on old gen = %v, want STALE", stale.Status)
	}
	snap, _ := a.Snapshot("r")
	if len(snap.Writes) != 1 {
		t.Fatalf("writes = %q, duplicate must not re-apply", snap.Writes)
	}
}

func TestRenewCannotResurrect(t *testing.T) {
	a, _ := newTestArbiter(10 * time.Second)
	gen, _ := a.Acquire("r", "alice")
	if _, err := a.Renew("r", "alice", gen); err != nil {
		t.Fatalf("renew current gen: %v", err)
	}
	a.Acquire("r", "bob")
	if _, err := a.Renew("r", "alice", gen); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("renew old gen err = %v, want ErrStaleGeneration", err)
	}
}

func TestRenewExpiredButCurrentLease(t *testing.T) {
	a, clock := newTestArbiter(10 * time.Second)
	gen, _ := a.Acquire("r", "alice")
	clock.Advance(30 * time.Second) // clock jump past expiry
	if got := a.Write("r", "alice", gen, "req-1", "x"); got.Status != StatusExpired {
		t.Fatalf("write after jump = %v, want EXPIRED", got)
	}
	if _, err := a.Renew("r", "alice", gen); err != nil {
		t.Fatalf("renew expired-but-current lease: %v", err)
	}
	if got := a.Write("r", "alice", gen, "req-1", "x"); got.Status != StatusApplied {
		t.Fatalf("write after renew = %v, want APPLIED", got)
	}
}

func TestConcurrentHandoff(t *testing.T) {
	a, _ := newTestArbiter(time.Minute)
	gen, _ := a.Acquire("r", "alice")
	var wg sync.WaitGroup
	applied := map[Status]int{}
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) { // alice retries her write with the old generation
			defer wg.Done()
			res := a.Write("r", "alice", gen, fmt.Sprintf("req-%d", i), "a")
			mu.Lock()
			applied[res.Status]++
			mu.Unlock()
		}(i)
		go func() { // bob keeps acquiring, fencing alice
			defer wg.Done()
			a.Acquire("r", "bob")
		}()
	}
	wg.Wait()
	if applied[StatusStale] == 0 {
		t.Fatal("expected some alice writes to be fenced as STALE")
	}
	t.Logf("outcomes: %v", applied)
}
