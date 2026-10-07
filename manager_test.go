package resman

import (
	"context"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

func waitCond(t *testing.T, timeout time.Duration, f func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %v: %s", timeout, msg)
}

// TestNoPartialGrant: a request for {a,b} must not be granted while only
// one of the two resources is available.
func TestNoPartialGrant(t *testing.T) {
	m := New(Vector{"a": 1, "b": 1})
	holder, err := m.Acquire(context.Background(), Vector{"b": 1})
	if err != nil {
		t.Fatal(err)
	}

	granted := make(chan *Grant, 1)
	go func() {
		g, err := m.Acquire(context.Background(), Vector{"a": 1, "b": 1})
		if err != nil {
			t.Errorf("acquire: %v", err)
			return
		}
		granted <- g
	}()

	waitCond(t, time.Second, func() bool { return m.Queued() == 1 }, "request queued")
	select {
	case <-granted:
		t.Fatal("partial grant: request granted while b unavailable")
	case <-time.After(100 * time.Millisecond):
	}
	if got := m.Snapshot(); !reflect.DeepEqual(got, Vector{"a": 1, "b": 0}) {
		t.Fatalf("avail changed while waiting: %v", got)
	}

	holder.Release()
	select {
	case g := <-granted:
		if want := (Vector{"a": 1, "b": 1}); !reflect.DeepEqual(g.Vector(), want) {
			t.Fatalf("grant = %v, want full %v", g.Vector(), want)
		}
		g.Release()
	case <-time.After(time.Second):
		t.Fatal("request not granted after release")
	}
	if got := m.Snapshot(); !reflect.DeepEqual(got, Vector{"a": 1, "b": 1}) {
		t.Fatalf("final avail = %v, want full capacity", got)
	}
}

// TestFIFOHeadOfLineBlocking: a satisfiable request must not overtake a
// blocked predecessor.
func TestFIFOHeadOfLineBlocking(t *testing.T) {
	m := New(Vector{"a": 2})
	g1, _ := m.Acquire(context.Background(), Vector{"a": 1})
	g2, _ := m.Acquire(context.Background(), Vector{"a": 1})

	r1 := make(chan *Grant, 1)
	go func() {
		g, err := m.Acquire(context.Background(), Vector{"a": 2})
		if err != nil {
			t.Errorf("r1: %v", err)
			return
		}
		r1 <- g
	}()
	waitCond(t, time.Second, func() bool { return m.Queued() == 1 }, "r1 queued")

	r2 := make(chan *Grant, 1)
	go func() {
		g, err := m.Acquire(context.Background(), Vector{"a": 1})
		if err != nil {
			t.Errorf("r2: %v", err)
			return
		}
		r2 <- g
	}()
	waitCond(t, time.Second, func() bool { return m.Queued() == 2 }, "r2 queued")

	// Free one token: r2 alone would fit, but r1 is ahead and blocked.
	g1.Release()
	select {
	case <-r2:
		t.Fatal("r2 overtook blocked head r1")
	case <-time.After(100 * time.Millisecond):
	}
	if n := m.Queued(); n != 2 {
		t.Fatalf("queued = %d, want 2", n)
	}

	// Free the second token: head r1 is granted, then r2 after r1 releases.
	g2.Release()
	select {
	case g := <-r1:
		g.Release()
	case <-time.After(time.Second):
		t.Fatal("r1 not granted")
	}
	select {
	case g := <-r2:
		g.Release()
	case <-time.After(time.Second):
		t.Fatal("r2 not granted after r1")
	}
	if got := m.Snapshot(); got["a"] != 2 {
		t.Fatalf("final avail = %v, want 2", got)
	}
}

// TestCancelWaitingRequest: cancellation dequeues a waiter without
// touching tokens.
func TestCancelWaitingRequest(t *testing.T) {
	m := New(Vector{"a": 1})
	holder, _ := m.Acquire(context.Background(), Vector{"a": 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.Acquire(ctx, Vector{"a": 1})
		done <- err
	}()
	waitCond(t, time.Second, func() bool { return m.Queued() == 1 }, "request queued")

	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := m.Queued(); n != 0 {
		t.Fatalf("queued = %d, want 0", n)
	}
	if got := m.Snapshot(); got["a"] != 0 {
		t.Fatalf("avail = %v, tokens leaked on cancel", got)
	}
	holder.Release()
	if got := m.Snapshot(); got["a"] != 1 {
		t.Fatalf("avail = %v after release, want 1", got)
	}
}

// TestCancelGrantedKeepsTokens: cancelling the context of an
// already-granted request must not free in-use tokens; only an explicit
// Release returns them.
func TestCancelGrantedKeepsTokens(t *testing.T) {
	m := New(Vector{"a": 1})
	ctx, cancel := context.WithCancel(context.Background())
	g, err := m.Acquire(ctx, Vector{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	if got := m.Snapshot(); got["a"] != 0 {
		t.Fatalf("avail = %v after cancel, tokens must stay allocated", got)
	}

	// A new waiter stays blocked until the explicit Release.
	waiter := make(chan *Grant, 1)
	go func() {
		g, err := m.Acquire(context.Background(), Vector{"a": 1})
		if err != nil {
			t.Errorf("waiter: %v", err)
			return
		}
		waiter <- g
	}()
	waitCond(t, time.Second, func() bool { return m.Queued() == 1 }, "waiter queued")
	select {
	case <-waiter:
		t.Fatal("waiter granted without explicit release")
	case <-time.After(100 * time.Millisecond):
	}

	g.Release()
	select {
	case wg := <-waiter:
		wg.Release()
	case <-time.After(time.Second):
		t.Fatal("waiter not granted after explicit release")
	}
	if got := m.Snapshot(); got["a"] != 1 {
		t.Fatalf("final avail = %v, want 1", got)
	}
}

// TestImpossibleRequest: requests that can never be satisfied fail fast.
func TestImpossibleRequest(t *testing.T) {
	m := New(Vector{"a": 1})
	for _, need := range []Vector{
		{"a": 2},
		{"b": 1},
	} {
		if _, err := m.Acquire(context.Background(), need); err == nil {
			t.Fatalf("need %v: expected error", need)
		} else if _, ok := err.(*ImpossibleError); !ok {
			t.Fatalf("need %v: err = %T, want *ImpossibleError", need, err)
		}
	}
	if _, err := m.Acquire(context.Background(), Vector{"a": -1}); err == nil {
		t.Fatal("negative amount: expected error")
	}
	if n := m.Queued(); n != 0 {
		t.Fatalf("queued = %d, want 0", n)
	}
}

// TestDoubleRelease: Release is idempotent and cannot create tokens.
func TestDoubleRelease(t *testing.T) {
	m := New(Vector{"a": 1})
	g, _ := m.Acquire(context.Background(), Vector{"a": 1})
	g.Release()
	g.Release()
	if got := m.Snapshot(); got["a"] != 1 {
		t.Fatalf("avail = %v, double release created tokens", got)
	}
}

// TestConservationRace hammers the manager with concurrent
// acquire/cancel/release traffic. The manager panics the moment the
// conservation invariant (0 <= avail <= capacity) breaks, the race
// detector catches unsynchronized access, and the final state must show
// every token back: no leaks, no partial grants, no creation.
func TestConservationRace(t *testing.T) {
	cap := Vector{"cpu": 8, "mem": 16, "disk": 4}
	m := New(cap)
	names := []string{"cpu", "mem", "disk"}

	var wg sync.WaitGroup
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				need := Vector{}
				for _, name := range names {
					if n := rng.Int63n(cap[name]/2 + 1); n > 0 {
						need[name] = n
					}
				}
				ctx := context.Background()
				if rng.Intn(4) == 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Duration(rng.Intn(5))*time.Millisecond)
					defer cancel()
				}
				g, err := m.Acquire(ctx, need)
				if err != nil {
					continue
				}
				// All-or-nothing: the grant must match the request exactly.
				if !reflect.DeepEqual(g.Vector(), need) {
					t.Errorf("grant = %v, want %v", g.Vector(), need)
				}
				time.Sleep(time.Duration(rng.Intn(2)) * time.Millisecond)
				g.Release()
			}
		}(int64(w))
	}

	// Concurrent observer: available tokens must stay within bounds.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			snap := m.Snapshot()
			for name, c := range cap {
				if a := snap[name]; a < 0 || a > c {
					t.Errorf("conservation violated: %s avail=%d capacity=%d", name, a, c)
				}
			}
		}
	}()

	wg.Wait()
	close(stop)
	if got := m.Snapshot(); !reflect.DeepEqual(got, cap) {
		t.Fatalf("final avail = %v, want full capacity %v (token leak)", got, cap)
	}
	if n := m.Queued(); n != 0 {
		t.Fatalf("queued = %d, want 0", n)
	}
}
