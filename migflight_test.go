package migflight

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// waitDups blocks until key has an in-flight call with at least n waiters.
func waitDups(t *testing.T, g *Group, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		c, ok := g.calls[key]
		dups := 0
		if ok {
			dups = c.dups
		}
		g.mu.Unlock()
		if ok && dups >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d waiters on %q", n, key)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestShare: concurrent requests for the same key share one execution.
func TestShare(t *testing.T) {
	g := NewGroup()
	var calls int32
	var wg sync.WaitGroup
	results := make([]Result, 10)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = g.Do("k", func(h *Handle) (any, error) {
				atomic.AddInt32(&calls, 1)
				time.Sleep(20 * time.Millisecond)
				return "v", nil
			})
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("fn executed %d times, want 1", got)
	}
	shared := 0
	for i, r := range results {
		if r.Val != "v" || r.Err != nil {
			t.Fatalf("result %d = %v, %v", i, r.Val, r.Err)
		}
		if r.Shared {
			shared++
		}
	}
	if shared != 9 {
		t.Fatalf("shared count = %d, want 9", shared)
	}
}

// TestPromoteClaimsFormalKey: a temp call promotes to a free formal key;
// later requests for the formal key join the migrated call.
func TestPromoteClaimsFormalKey(t *testing.T) {
	g := NewGroup()
	started := make(chan struct{})
	proceed := make(chan struct{})
	release := make(chan struct{})

	leaderDone := make(chan Result, 1)
	go func() {
		leaderDone <- g.Do("tmp", func(h *Handle) (any, error) {
			close(started)
			<-proceed
			if merged := h.Promote("formal"); merged {
				t.Errorf("Promote merged into existing call, want claim")
			}
			<-release
			return "formal-val", nil
		})
	}()
	waitFor(t, started, "leader start")

	// Waiter on the temp key joins before promotion.
	tmpWaiter := make(chan Result, 1)
	go func() { tmpWaiter <- g.Do("tmp", nil) }()
	time.Sleep(20 * time.Millisecond)

	close(proceed)
	time.Sleep(20 * time.Millisecond)

	// After promotion, a request for the formal key joins the same call.
	formalWaiter := make(chan Result, 1)
	go func() { formalWaiter <- g.Do("formal", nil) }()
	time.Sleep(20 * time.Millisecond)

	close(release)

	want := Result{Val: "formal-val"}
	if r := <-leaderDone; r.Val != want.Val || r.Err != nil {
		t.Fatalf("leader got %v, %v", r.Val, r.Err)
	}
	for name, ch := range map[string]chan Result{"tmp": tmpWaiter, "formal": formalWaiter} {
		select {
		case r := <-ch:
			if r.Val != want.Val || r.Err != nil || !r.Shared {
				t.Fatalf("%s waiter got %+v", name, r)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s waiter did not unblock", name)
		}
	}
}

// TestPromoteMergesIntoExisting: the formal key is busy, so the temp call's
// waiters are merged and receive the formal call's result.
func TestPromoteMergesIntoExisting(t *testing.T) {
	g := NewGroup()
	formalStarted := make(chan struct{})
	formalRelease := make(chan struct{})
	tmpStarted := make(chan struct{})
	tmpProceed := make(chan struct{})
	tmpRelease := make(chan struct{})

	formalDone := make(chan Result, 1)
	go func() {
		formalDone <- g.Do("formal", func(h *Handle) (any, error) {
			close(formalStarted)
			<-formalRelease
			return "from-formal", nil
		})
	}()
	waitFor(t, formalStarted, "formal start")

	tmpDone := make(chan Result, 1)
	go func() {
		tmpDone <- g.Do("tmp", func(h *Handle) (any, error) {
			close(tmpStarted)
			<-tmpProceed
			if merged := h.Promote("formal"); !merged {
				t.Errorf("Promote did not merge into existing formal call")
			}
			<-tmpRelease
			return "from-tmp", nil // discarded for waiters
		})
	}()
	waitFor(t, tmpStarted, "tmp start")

	tmpWaiter := make(chan Result, 1)
	go func() { tmpWaiter <- g.Do("tmp", nil) }()
	time.Sleep(20 * time.Millisecond)

	close(tmpProceed) // tmp promotes and merges into formal
	time.Sleep(20 * time.Millisecond)

	close(tmpRelease)    // tmp's fn finishes; its waiters must NOT wake with its result
	close(formalRelease) // formal finishes; merged waiters get its result

	if r := <-tmpDone; r.Val != "from-tmp" {
		t.Fatalf("tmp leader got %v, want its own value from-tmp", r.Val)
	}
	select {
	case r := <-tmpWaiter:
		if r.Val != "from-formal" || r.Err != nil {
			t.Fatalf("merged waiter got %+v, want from-formal", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("merged waiter did not unblock")
	}
	if r := <-formalDone; r.Val != "from-formal" {
		t.Fatalf("formal leader got %v", r.Val)
	}
}

// TestConcurrentPromoteSameFormalKey: two temp keys migrate to the same
// formal key at the same time. Exactly one claims it; the other merges.
// Waiters of both temp keys end up with the winner's result. No deadlock.
func TestConcurrentPromoteSameFormalKey(t *testing.T) {
	for iter := 0; iter < 300; iter++ {
		g := NewGroup()
		started := make(chan string, 2)
		proceed := make(chan struct{})
		release := make(chan struct{})
		merged1 := make(chan bool, 1)
		merged2 := make(chan bool, 1)

		run := func(key, val string, merged chan<- bool, done chan<- Result) {
			done <- g.Do(key, func(h *Handle) (any, error) {
				started <- key
				<-proceed
				merged <- h.Promote("formal")
				<-release
				return val, nil
			})
		}
		done1 := make(chan Result, 1)
		done2 := make(chan Result, 1)
		go run("tmp1", "v1", merged1, done1)
		go run("tmp2", "v2", merged2, done2)
		<-started
		<-started

		w1 := make(chan Result, 1)
		w2 := make(chan Result, 1)
		go func() { w1 <- g.Do("tmp1", nil) }()
		go func() { w2 <- g.Do("tmp2", nil) }()
		waitDups(t, g, "tmp1", 1)
		waitDups(t, g, "tmp2", 1)

		close(proceed) // both promote concurrently
		m1, m2 := <-merged1, <-merged2
		if m1 == m2 {
			t.Fatalf("iter %d: merged flags %v,%v — exactly one call must claim the formal key", iter, m1, m2)
		}
		close(release)

		r1 := <-done1
		r2 := <-done2
		// The winner's value is the one whose Promote returned false.
		win := "v1"
		if m1 {
			win = "v2"
		}
		for name, ch := range map[string]chan Result{"w1": w1, "w2": w2} {
			select {
			case r := <-ch:
				if r.Val != win {
					t.Fatalf("iter %d: %s got %v, want winner %v", iter, name, r.Val, win)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("iter %d: %s did not unblock (deadlock?)", iter, name)
			}
		}
		if r1.Val == r2.Val {
			t.Fatalf("iter %d: leaders must keep their own values, both got %v", iter, r1.Val)
		}
	}
}

// TestOldKeyNewGeneration: after the temp key migrates away, new requests
// for it start a fresh, explicitly numbered generation.
func TestOldKeyNewGeneration(t *testing.T) {
	g := NewGroup()
	started := make(chan struct{})
	proceed := make(chan struct{})
	release := make(chan struct{})

	gen0 := make(chan uint64, 1)
	go func() {
		g.Do("tmp", func(h *Handle) (any, error) {
			gen0 <- h.Gen()
			close(started)
			<-proceed
			h.Promote("formal")
			<-release
			return "old-gen", nil
		})
	}()
	waitFor(t, started, "first call start")
	if g := <-gen0; g != 0 {
		t.Fatalf("first generation = %d, want 0", g)
	}
	close(proceed)
	time.Sleep(20 * time.Millisecond)

	// New request on the old key: must run a fresh function in generation 1.
	ran := make(chan uint64, 1)
	second := make(chan Result, 1)
	go func() {
		second <- g.Do("tmp", func(h *Handle) (any, error) {
			ran <- h.Gen()
			return "new-gen", nil
		})
	}()
	select {
	case g := <-ran:
		if g != 1 {
			t.Fatalf("second generation = %d, want 1", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new request on old key did not start a new generation")
	}
	if r := <-second; r.Val != "new-gen" {
		t.Fatalf("second call got %v", r.Val)
	}
	close(release)
}

// TestCancelWaiter: cancelling one waiter never affects the others.
func TestCancelWaiter(t *testing.T) {
	g := NewGroup()
	started := make(chan struct{})
	release := make(chan struct{})

	go g.Do("k", func(h *Handle) (any, error) {
		close(started)
		<-release
		return "v", nil
	})
	waitFor(t, started, "leader start")

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan Result, 1)
	go func() { cancelled <- g.DoCtx(ctx, "k", nil) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case r := <-cancelled:
		if !errors.Is(r.Err, context.Canceled) {
			t.Fatalf("cancelled waiter err = %v, want context.Canceled", r.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}

	// Other waiters still get the real result.
	ok := make(chan Result, 1)
	go func() { ok <- g.Do("k", nil) }()
	time.Sleep(20 * time.Millisecond)
	close(release)
	select {
	case r := <-ok:
		if r.Val != "v" || r.Err != nil {
			t.Fatalf("surviving waiter got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("surviving waiter affected by cancellation")
	}
}

// TestPromoteAfterFinish: promoting a finished call is a no-op.
func TestPromoteAfterFinish(t *testing.T) {
	g := NewGroup()
	var h *Handle
	g.Do("k", func(hh *Handle) (any, error) {
		h = hh
		return "v", nil
	})
	if h.Promote("formal") {
		t.Fatal("Promote after finish reported merge")
	}
	// Formal key must be free and usable.
	r := g.Do("formal", func(hh *Handle) (any, error) { return "f", nil })
	if r.Val != "f" {
		t.Fatalf("formal key got %v", r.Val)
	}
}
