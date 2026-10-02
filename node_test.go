package gracefulleave

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testQuiescent = 150 * time.Millisecond

func TestLeaveBroadcastMarksPeersLeaving(t *testing.T) {
	c := NewCluster([]string{"n1", "n2", "n3"}, testQuiescent, nil)
	c.Node("n3").Leave()

	for _, id := range []string{"n1", "n2"} {
		m, ok := c.Node(id).Membership().Get("n3")
		if !ok {
			t.Fatalf("%s: no entry for n3 during quiescent period", id)
		}
		if m.State() != StateLeaving {
			t.Fatalf("%s: n3 state = %v, want Leaving", id, m.State())
		}
		for _, r := range c.Node(id).Membership().Ready() {
			if r.ID == "n3" {
				t.Fatalf("%s: n3 still in ready pool", id)
			}
		}
	}
}

func TestRouterSkipsLeavingMember(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	counting := func(id string) Handler {
		return func(ctx context.Context, req any) (any, error) {
			mu.Lock()
			hits[id]++
			mu.Unlock()
			return id, nil
		}
	}
	tport := NewTransport()
	n1 := NewNode("n1", "addr-n1", tport, testQuiescent, counting("n1"))
	n2 := NewNode("n2", "addr-n2", tport, testQuiescent, counting("n2"))
	n3 := NewNode("n3", "addr-n3", tport, testQuiescent, counting("n3"))
	for _, p := range []*Node{n2, n3} {
		n1.Membership().Add(NewMember(p.ID, p.Addr))
	}

	// Baseline: round-robin across both peers.
	for i := 0; i < 10; i++ {
		if _, err := n1.Router().Call(context.Background(), "ping"); err != nil {
			t.Fatalf("baseline call %d: %v", i, err)
		}
	}
	mu.Lock()
	base2, base3 := hits["n2"], hits["n3"]
	mu.Unlock()
	if base2 == 0 || base3 == 0 {
		t.Fatalf("baseline hits n2=%d n3=%d, want both > 0", base2, base3)
	}

	// n3 leaves: the quiescent period must block all new traffic to it.
	n3.Leave()
	for i := 0; i < 20; i++ {
		if _, err := n1.Router().Call(context.Background(), "ping"); err != nil {
			t.Fatalf("call %d after leave: %v", i, err)
		}
	}
	mu.Lock()
	got2, got3 := hits["n2"], hits["n3"]
	mu.Unlock()
	if got3 != base3 {
		t.Fatalf("n3 received %d new calls while Leaving; want 0", got3-base3)
	}
	if got2 != base2+20 {
		t.Fatalf("n2 received %d calls after leave, want %d", got2-base2, 20)
	}
}

func TestInFlightCallCompletesDuringQuiescent(t *testing.T) {
	release := make(chan struct{})
	slow := func(ctx context.Context, req any) (any, error) {
		<-release // simulate a long-running RPC
		return "done", nil
	}
	c := NewCluster([]string{"n1", "n2"}, 500*time.Millisecond, slow)

	// Start an in-flight call to n2, then have n2 leave while it runs.
	resCh := make(chan any, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := c.Transport.Call(context.Background(), "n2", "req")
		resCh <- res
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond) // let the call land on n2
	c.Node("n2").Leave()
	close(release) // let the in-flight RPC finish well inside the quiescent window

	if err := <-errCh; err != nil {
		t.Fatalf("in-flight RPC failed after leave: %v", err)
	}
	if res := <-resCh; res != "done" {
		t.Fatalf("in-flight RPC result = %v, want done", res)
	}
}

func TestEntryPhysicallyRemovedAfterQuiescent(t *testing.T) {
	c := NewCluster([]string{"n1", "n2", "n3"}, testQuiescent, nil)
	c.Node("n3").Leave()

	for _, id := range []string{"n1", "n2"} {
		id := id
		waitFor(t, id+" drops n3 routing entry", func() bool {
			_, ok := c.Node(id).Membership().Get("n3")
			return !ok
		})
	}
	// The leaving node itself shuts down and deregisters from the transport.
	waitFor(t, "n3 deregisters from transport", func() bool {
		_, err := c.Transport.Call(context.Background(), "n3", "ping")
		return errors.Is(err, ErrNodeUnreachable)
	})
}

func TestNoSuddenFailuresUnderLoad(t *testing.T) {
	c := NewCluster([]string{"n1", "n2", "n3"}, 300*time.Millisecond, nil)

	var calls, failures atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, err := c.Node("n1").Router().Call(context.Background(), "ping")
				calls.Add(1)
				if err != nil && !errors.Is(err, ErrNoReadyMembers) {
					failures.Add(1)
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	c.Node("n3").Leave() // leave mid-traffic
	time.Sleep(50 * time.Millisecond)
	c.Node("n2").Leave()
	time.Sleep(400 * time.Millisecond) // outlast both quiescent periods
	close(stop)
	wg.Wait()

	if calls.Load() == 0 {
		t.Fatal("no calls were made")
	}
	if f := failures.Load(); f != 0 {
		t.Fatalf("%d/%d calls failed during graceful leave; want zero sudden RPC failures", f, calls.Load())
	}
	t.Logf("%d calls completed with zero failures while n2,n3 left", calls.Load())
}

func TestCallToRemovedNodeFailsFast(t *testing.T) {
	c := NewCluster([]string{"n1", "n2"}, testQuiescent, nil)
	c.Node("n2").Leave()

	waitFor(t, "n2 endpoint gone", func() bool {
		_, err := c.Transport.Call(context.Background(), "n2", "ping")
		return errors.Is(err, ErrNodeUnreachable)
	})

	start := time.Now()
	_, err := c.Transport.Call(context.Background(), "n2", "ping")
	if !errors.Is(err, ErrNodeUnreachable) {
		t.Fatalf("err = %v, want ErrNodeUnreachable", err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("call to removed node took %v; must fail fast, not time out", d)
	}
}

func TestLeaveAndShutdownIdempotent(t *testing.T) {
	c := NewCluster([]string{"n1", "n2"}, testQuiescent, nil)
	n := c.Node("n2")
	n.Leave()
	n.Leave() // must not panic or double-broadcast
	n.Shutdown()
	n.Shutdown()

	if _, err := c.Transport.Call(context.Background(), "n2", "ping"); !errors.Is(err, ErrNodeUnreachable) {
		t.Fatalf("err = %v, want ErrNodeUnreachable after shutdown", err)
	}
	// Peers still remove the entry via their own quiescent timers.
	waitFor(t, "n1 drops n2 entry", func() bool {
		_, ok := c.Node("n1").Membership().Get("n2")
		return !ok
	})
}

func TestShutdownDrainsInFlight(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	slow := func(ctx context.Context, req any) (any, error) {
		close(started)
		<-release
		return "ok", nil
	}
	c := NewCluster([]string{"n1", "n2"}, time.Hour, slow)

	resCh := make(chan error, 1)
	go func() {
		res, err := c.Transport.Call(context.Background(), "n2", "req")
		if res != "ok" {
			err = fmt.Errorf("result = %v, want ok", res)
		}
		resCh <- err
	}()
	<-started

	done := make(chan struct{})
	go func() { c.Node("n2").Shutdown(); close(done) }()

	select {
	case <-done:
		t.Fatal("Shutdown returned while an RPC was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-done
	if err := <-resCh; err != nil {
		t.Fatalf("in-flight RPC: %v", err)
	}
}
