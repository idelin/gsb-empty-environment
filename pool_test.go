package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAcquireReleaseReusesIdle(t *testing.T) {
	var created atomic.Int32
	p := New(2,
		func(ctx context.Context) (any, error) { return created.Add(1), nil },
		func(res any) {},
	)
	l1, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := l1.Value().(int32); got != 1 {
		t.Fatalf("first lease = %d, want 1", got)
	}
	if err := l1.Release(false); err != nil {
		t.Fatal(err)
	}
	l2, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := l2.Value().(int32); got != 1 {
		t.Fatalf("second lease = %d, want reused 1", got)
	}
	if n := created.Load(); n != 1 {
		t.Fatalf("factory called %d times, want 1", n)
	}
	l2.Release(false)
}

func TestLimitIncludesCreating(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 4)
	gate := make(chan struct{})
	p := New(2,
		func(ctx context.Context) (any, error) {
			calls.Add(1)
			started <- struct{}{}
			<-gate // slow creation under test control
			return struct{}{}, nil
		},
		func(res any) {},
	)
	var wg sync.WaitGroup
	leases := make(chan *Lease, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := p.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			leases <- l
		}()
	}
	<-started
	<-started

	// Both slots are held by in-flight creations; a third acquire must
	// wait and then fail by deadline instead of starting a creation.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third acquire err = %v, want DeadlineExceeded", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("factory calls = %d, want 2 (creating counts toward limit)", n)
	}

	close(gate)
	wg.Wait()
	l1, l2 := <-leases, <-leases
	l1.Release(false)
	l2.Release(false)
}

func TestCancelReclaimsResources(t *testing.T) {
	const n = 100
	var started, created, destroyed atomic.Int32
	gate := make(chan struct{})
	p := New(n,
		func(ctx context.Context) (any, error) {
			started.Add(1)
			<-gate // creation completes only when the test allows
			return created.Add(1), nil
		},
		func(res any) { destroyed.Add(1) },
	)

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Acquire(ctx)
			errs <- err
		}()
	}
	waitFor(t, "100 creations to start", func() bool { return started.Load() == n })

	cancel()
	wg.Wait()
	for i := 0; i < n; i++ {
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Fatalf("acquire err = %v, want context.Canceled", err)
		}
	}

	// Let every in-flight creation finish: each late resource must be
	// reclaimed, so cancelling 100 times leaves 0 background resources.
	close(gate)
	waitFor(t, "100 late resources reclaimed", func() bool { return destroyed.Load() == n })
	if got := created.Load(); got != n {
		t.Fatalf("created = %d, want %d", got, n)
	}
	waitFor(t, "creation slots freed", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.creating == 0
	})

	// The pool is still fully usable: all 100 slots were freed.
	l, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l.Release(false)
}

func TestFactoryErrorFreesSlot(t *testing.T) {
	boom := errors.New("boom")
	var calls atomic.Int32
	p := New(1,
		func(ctx context.Context) (any, error) {
			if calls.Add(1) == 1 {
				return nil, boom
			}
			return "ok", nil
		},
		func(res any) {},
	)
	if _, err := p.Acquire(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	l, err := p.Acquire(context.Background()) // slot must have been freed
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Value().(string); got != "ok" {
		t.Fatalf("value = %q, want ok", got)
	}
	l.Release(false)
}

func TestBadResourceDestroyedNotReused(t *testing.T) {
	var created, destroyed atomic.Int32
	p := New(1,
		func(ctx context.Context) (any, error) { return created.Add(1), nil },
		func(res any) { destroyed.Add(1) },
	)
	l, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(true); err != nil { // bad
		t.Fatal(err)
	}
	if got := destroyed.Load(); got != 1 {
		t.Fatalf("destroyed = %d, want 1", got)
	}
	l2, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := l2.Value().(int32); got != 2 {
		t.Fatalf("value = %d, want fresh resource 2 (bad one not reused)", got)
	}
	l2.Release(false)
}

func TestDoubleRelease(t *testing.T) {
	p := New(1,
		func(ctx context.Context) (any, error) { return struct{}{}, nil },
		func(res any) {},
	)
	l, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(false); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := l.Release(false); !errors.Is(err, ErrDoubleReturn) {
		t.Fatalf("second release err = %v, want ErrDoubleReturn", err)
	}
	if err := l.Release(true); !errors.Is(err, ErrDoubleReturn) {
		t.Fatalf("third release err = %v, want ErrDoubleReturn", err)
	}
}

func TestCloseWaitsForReturn(t *testing.T) {
	var destroyed atomic.Int32
	p := New(1,
		func(ctx context.Context) (any, error) { return struct{}{}, nil },
		func(res any) { destroyed.Add(1) },
	)
	l, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.Close(context.Background(), WaitForReturn) }()

	select {
	case <-done:
		t.Fatal("Close returned while a lease was still borrowed")
	case <-time.After(50 * time.Millisecond):
	}

	if err := l.Release(false); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Close err = %v", err)
	}
	if got := destroyed.Load(); got != 1 {
		t.Fatalf("destroyed = %d, want 1 (returned after close)", got)
	}
	if _, err := p.Acquire(context.Background()); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("acquire after close err = %v, want ErrPoolClosed", err)
	}
}

func TestCloseForce(t *testing.T) {
	var destroyed atomic.Int32
	p := New(2,
		func(ctx context.Context) (any, error) { return struct{}{}, nil },
		func(res any) { destroyed.Add(1) },
	)
	borrowed, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	idle, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	idle.Release(false)

	done := make(chan error, 1)
	go func() { done <- p.Close(context.Background(), Force) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Force close did not return promptly")
	}
	if got := destroyed.Load(); got != 1 {
		t.Fatalf("destroyed = %d, want 1 (idle destroyed at close)", got)
	}
	if err := borrowed.Release(false); err != nil {
		t.Fatal(err)
	}
	if got := destroyed.Load(); got != 2 {
		t.Fatalf("destroyed = %d, want 2 (outstanding destroyed on return)", got)
	}
}

func TestDoubleClose(t *testing.T) {
	p := New(1,
		func(ctx context.Context) (any, error) { return struct{}{}, nil },
		func(res any) {},
	)
	if err := p.Close(context.Background(), WaitForReturn); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background(), WaitForReturn); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("second close err = %v, want ErrPoolClosed", err)
	}
}

func TestConcurrentStress(t *testing.T) {
	var created, destroyed, attempts atomic.Int32
	p := New(8,
		func(ctx context.Context) (any, error) {
			attempt := attempts.Add(1)
			time.Sleep(time.Duration(attempt%3) * time.Millisecond)
			if attempt%11 == 0 {
				return nil, fmt.Errorf("injected failure %d", attempt)
			}
			return created.Add(1), nil
		},
		func(res any) { destroyed.Add(1) },
	)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(1+g%5)*time.Millisecond)
				l, err := p.Acquire(ctx)
				cancel()
				if err != nil {
					continue
				}
				_ = l.Release(i%7 == 0)
			}
		}(g)
	}
	wg.Wait()
	if err := p.Close(context.Background(), WaitForReturn); err != nil {
		t.Fatal(err)
	}
	// Every resource the factory produced is destroyed exactly once:
	// returned bad, returned after close, idle at close, or reaped.
	waitFor(t, "all created resources destroyed", func() bool {
		return destroyed.Load() == created.Load()
	})
}
