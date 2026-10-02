package distlock

import (
	"context"
	"testing"
	"time"
)

func newTestServer() *Server {
	return NewServer(WithSweepInterval(10 * time.Millisecond))
}

// Acceptance scenario: A holds the lock with heartbeats, A freezes, the
// lease expires on its TTL, queued B acquires, and recovered A is fenced
// out when it tries to write.
func TestLeaseExpiryHandsLockToWaiterAndFencesStaleHolder(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()
	store := NewStorage()
	ttl := 400 * time.Millisecond

	// Node A acquires the lock; heartbeat starts automatically.
	acquiredAt := time.Now()
	lockA, err := srv.Lock(context.Background(), "resource", ttl)
	if err != nil {
		t.Fatalf("A lock: %v", err)
	}
	tokenA := lockA.FenceToken()

	// Node B queues for the same lock.
	type result struct {
		lock *Lock
		err  error
	}
	bRes := make(chan result, 1)
	go func() {
		l, err := srv.Lock(context.Background(), "resource", ttl)
		bRes <- result{l, err}
	}()

	// Give B a moment to enqueue, then A freezes: heartbeat stops.
	time.Sleep(50 * time.Millisecond)
	lockA.SimulateCrash()

	// B must not get the lock before the lease TTL elapses...
	select {
	case r := <-bRes:
		if r.err != nil {
			t.Fatalf("B lock: %v", r.err)
		}
		elapsed := time.Since(acquiredAt)
		if elapsed < ttl-20*time.Millisecond {
			t.Fatalf("B acquired too early after %v; lease did not protect A", elapsed)
		}
		tokenB := r.lock.FenceToken()
		if tokenB <= tokenA {
			t.Fatalf("fence token not increasing: A=%d B=%d", tokenA, tokenB)
		}

		// B writes with the newer token: accepted.
		if err := store.Write(tokenB, "key", "from-B"); err != nil {
			t.Fatalf("write from B: %v", err)
		}
		// A recovers and tries to write with its old token: rejected,
		// because the resource has already fenced at tokenB.
		if err := store.Write(tokenA, "key", "from-A"); err != ErrStaleFence {
			t.Fatalf("stale write from A: got %v, want ErrStaleFence", err)
		}
		if v, _ := store.Read("key"); v != "from-B" {
			t.Fatalf("storage = %q, want from-B", v)
		}
		r.lock.Unlock()
	case <-time.After(5 * time.Second):
		t.Fatal("B never acquired the lock after A's lease expired")
	}
}

// Heartbeats keep the lease alive well beyond a single TTL.
func TestKeepAliveExtendsLease(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()
	ttl := 200 * time.Millisecond

	lockA, err := srv.Lock(context.Background(), "res", ttl)
	if err != nil {
		t.Fatalf("A lock: %v", err)
	}
	defer lockA.Unlock()

	// B waits with a deadline spanning several TTLs; A keeps renewing, so
	// B must time out without ever acquiring.
	ctx, cancel := context.WithTimeout(context.Background(), 3*ttl+100*time.Millisecond)
	defer cancel()
	if _, err := srv.Lock(ctx, "res", ttl); err != context.DeadlineExceeded {
		t.Fatalf("B lock: got %v, want DeadlineExceeded (A should stay owner)", err)
	}
	select {
	case <-lockA.Done():
		t.Fatal("A lost the lease despite heartbeats")
	default:
	}
}

// Unlock hands the lock to a queued waiter immediately, without waiting
// for the lease TTL.
func TestUnlockWakesWaiter(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()
	ttl := 5 * time.Second

	lockA, err := srv.Lock(context.Background(), "res", ttl)
	if err != nil {
		t.Fatalf("A lock: %v", err)
	}
	got := make(chan *Lock, 1)
	go func() {
		l, err := srv.Lock(context.Background(), "res", ttl)
		if err != nil {
			t.Errorf("B lock: %v", err)
			return
		}
		got <- l
	}()
	time.Sleep(50 * time.Millisecond)
	if err := lockA.Unlock(); err != nil {
		t.Fatalf("A unlock: %v", err)
	}
	select {
	case l := <-got:
		l.Unlock()
	case <-time.After(time.Second):
		t.Fatal("B was not woken promptly after Unlock")
	}
}

// After a lease expires, operations on the old handle fail.
func TestExpiredHandleCannotUnlock(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()
	ttl := 150 * time.Millisecond

	lockA, err := srv.Lock(context.Background(), "res", ttl)
	if err != nil {
		t.Fatalf("A lock: %v", err)
	}
	lockA.SimulateCrash()
	time.Sleep(2 * ttl)
	if err := lockA.Unlock(); err != ErrLockLost {
		t.Fatalf("unlock after expiry: got %v, want ErrLockLost", err)
	}
}

// Fencing tokens increase monotonically across acquisitions.
func TestFenceTokensIncrease(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()
	var prev uint64
	for i := 0; i < 3; i++ {
		l, err := srv.Lock(context.Background(), "res", time.Second)
		if err != nil {
			t.Fatalf("lock %d: %v", i, err)
		}
		if l.FenceToken() <= prev {
			t.Fatalf("token %d not greater than %d", l.FenceToken(), prev)
		}
		prev = l.FenceToken()
		l.Unlock()
	}
}
