package pool_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"resourcepool/pool"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// A healthy released resource is reused: no second create happens.
func TestReuse(t *testing.T) {
	h := &harness{}
	p := newPoolWithHarness(t, 2, h)
	ctx := context.Background()

	a := mustAcquire(t, p, ctx)
	if err := a.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	b := mustAcquire(t, p, ctx)
	if err := b.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	live, peak, created, closed := h.snapshot()
	if created != 1 || live != 1 || peak != 1 || closed != 0 {
		t.Fatalf("created=%d live=%d peak=%d closed=%d, want created=1 live=1 peak=1 closed=0",
			created, live, peak, closed)
	}
}

// Creating + in-use resources never exceed MaxOpen; idle resources are free.
func TestQuotaCountsCreatingAndInUse(t *testing.T) {
	const max = 2
	h := &harness{delay: 20 * time.Millisecond}
	p := newPoolWithHarness(t, max, h)

	var peak int32
	stop := make(chan struct{})
	var observer sync.WaitGroup
	observer.Add(1)
	go func() {
		defer observer.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s := p.Stats()
				cur := s.Creating + s.InUse
				for {
					old := atomic.LoadInt32(&peak)
					if int32(cur) <= old || atomic.CompareAndSwapInt32(&peak, old, int32(cur)) {
						break
					}
				}
				if cur > max {
					t.Errorf("creating+inuse=%d exceeds max %d", cur, max)
				}
				time.Sleep(time.Microsecond)
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hd, err := p.Acquire(context.Background())
			if err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
			_ = hd.Release()
		}()
	}
	wg.Wait()
	close(stop)
	observer.Wait()

	if atomic.LoadInt32(&peak) > max {
		t.Fatalf("observed peak creating+inuse=%d > %d", peak, max)
	}
}

// When creation fails the quota is released and waiters can still acquire.
func TestCreateFailureReleasesQuota(t *testing.T) {
	h := &harness{}
	h.setErr(errors.New("boom"))
	p := newPoolWithHarness(t, 1, h)

	_, err := p.Acquire(context.Background())
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err=%v, want boom", err)
	}
	h.setErr(nil)
	hd := mustAcquire(t, p, context.Background())
	if err := hd.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	live, _, created, _ := h.snapshot()
	if created != 1 || live != 1 {
		t.Fatalf("created=%d live=%d, want 1/1", created, live)
	}
}

// Cancelling while a resource is being created cancels the factory and leaves
// no background resources behind.
func TestCancelDuringCreate(t *testing.T) {
	h := &harness{gate: make(chan struct{})}
	p := newPoolWithHarness(t, 1, h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Acquire(ctx)
		done <- err
	}()

	waitFor(t, time.Second, func() bool { return p.Stats().Creating == 1 }, "create to start")
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire err=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Acquire did not return after cancellation")
	}

	// The factory must observe the cancelled context.
	select {
	case <-h.gate:
		t.Fatal("gate unexpectedly closed")
	default:
	}
	close(h.gate)

	waitFor(t, time.Second, func() bool { s := p.Stats(); return s.Creating == 0 && s.InUse == 0 },
		"quota to be released")
	live, peak, created, closed := h.snapshot()
	if live != 0 || peak != 0 || created != 0 || closed != 0 {
		t.Fatalf("live=%d peak=%d created=%d closed=%d, want all zero", live, peak, created, closed)
	}

	// Quota is usable again.
	hd := mustAcquire(t, p, context.Background())
	_ = hd.Release()
}

// Even when the factory ignores cancellation and finishes successfully, the
// resource must be destroyed rather than leaked or reused.
func TestCancelCompletionRaceReclaimsResource(t *testing.T) {
	for iter := 0; iter < 300; iter++ {
		gate := make(chan struct{})
		h := &harness{}
		// Factory ignores ctx: it produces the resource first, then blocks
		// until the test releases it, so completion races with cancellation.
		create := func(ctx context.Context) (any, error) {
			r, err := h.create(context.Background())
			if err != nil {
				return nil, err
			}
			<-gate
			return r, nil
		}
		p := pool.New(pool.Config{MaxOpen: 1, Create: create, Close: h.closeRes})

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := p.Acquire(ctx)
			done <- err
		}()

		waitFor(t, time.Second, func() bool {
			_, _, created, _ := h.snapshot()
			return created == 1 && p.Stats().Creating == 1
		}, "resource to be created and factory to block")
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Acquire err=%v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Acquire did not unblock on cancel")
		}
		// Factory finishes successfully after the acquirer already left.
		close(gate)

		waitFor(t, time.Second, func() bool {
			live, peak, created, closed := h.snapshot()
			s := p.Stats()
			return s.Creating == 0 && s.InUse == 0 && s.Idle == 0 &&
				created == 1 && closed == 1 && live == 0 && peak == 1
		}, "raced resource to be reclaimed")

		// Next acquire must create a fresh resource; the reclaimed one never
		// entered the idle pool.
		hd := mustAcquire(t, p, context.Background())
		_ = hd.Release()
		live, _, created, _ := h.snapshot()
		if created != 2 || live != 1 {
			t.Fatalf("iter %d: created=%d live=%d, want 2/1", iter, created, live)
		}
	}
}

// 100 cancelled creates (limit 1) must not leave 100 background resources.
func TestCancel100TimesNoLeak(t *testing.T) {
	h := &harness{delay: 10 * time.Millisecond}
	p := newPoolWithHarness(t, 1, h)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i%5)*time.Millisecond)
		go func() {
			defer wg.Done()
			defer cancel()
			_, _ = p.Acquire(ctx)
		}()
		runtime.Gosched()
	}
	wg.Wait()

	waitFor(t, 2*time.Second, func() bool { return p.Stats().Creating == 0 }, "creates to settle")
	live, peak, created, closed := h.snapshot()
	if live != 0 {
		t.Fatalf("leaked %d background resources", live)
	}
	if peak > 1 {
		t.Fatalf("peak live resources=%d, quota was 1", peak)
	}
	if created != closed {
		t.Fatalf("created=%d but closed=%d, every created resource must be destroyed", created, closed)
	}

	hd := mustAcquire(t, p, context.Background())
	_ = hd.Release()
}

// A bad resource is destroyed on release and a new one is created afterwards.
func TestBadResourceDestroyed(t *testing.T) {
	h := &harness{}
	p := newPoolWithHarness(t, 2, h)

	hd := mustAcquire(t, p, context.Background())
	hd.MarkBad()
	if err := hd.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	live, peak, created, closed := h.snapshot()
	if created != 1 || closed != 1 || live != 0 || peak != 1 {
		t.Fatalf("created=%d closed=%d live=%d peak=%d, want 1/1/0/1", created, closed, live, peak)
	}

	hd2 := mustAcquire(t, p, context.Background())
	_ = hd2.Release()
	if _, _, created, _ := h.snapshot(); created != 2 {
		t.Fatalf("created=%d, want 2", created)
	}
}

// Releasing the same handle twice reports an error.
func TestDoubleRelease(t *testing.T) {
	h := &harness{}
	p := newPoolWithHarness(t, 1, h)

	hd := mustAcquire(t, p, context.Background())
	if err := hd.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := hd.Release(); !errors.Is(err, pool.ErrDoubleRelease) {
		t.Fatalf("second Release err=%v, want ErrDoubleRelease", err)
	}
	if err := hd.Release(); !errors.Is(err, pool.ErrDoubleRelease) {
		t.Fatalf("third Release err=%v, want ErrDoubleRelease", err)
	}
}

// Close waits for borrowed resources to come back.
func TestCloseWaitsForBorrowed(t *testing.T) {
	h := &harness{}
	p := pool.New(pool.Config{MaxOpen: 2, Create: h.create, Close: h.closeRes})

	hd := mustAcquire(t, p, context.Background())

	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()

	select {
	case <-closed:
		t.Fatal("Close returned before resource was released")
	case <-time.After(30 * time.Millisecond):
	}

	if err := hd.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not return after release")
	}

	if _, err := p.Acquire(context.Background()); !errors.Is(err, pool.ErrClosed) {
		t.Fatalf("Acquire after Close err=%v, want ErrClosed", err)
	}
	live, _, created, closedN := h.snapshot()
	if live != 0 || created != 1 || closedN != 1 {
		t.Fatalf("live=%d created=%d closed=%d, want 0/1/1", live, created, closedN)
	}
}

// CloseForce reclaims borrowed resources immediately and cancels creates.
func TestCloseForce(t *testing.T) {
	gate1 := make(chan struct{})
	h := &harness{}
	h.setGate(gate1)
	p := pool.New(pool.Config{MaxOpen: 2, Create: h.create, Close: h.closeRes})

	go func() {
		waitFor(t, time.Second, func() bool { return p.Stats().Creating == 1 }, "first create to start")
		close(gate1)
	}()
	hd := mustAcquire(t, p, context.Background())

	// A second create starts and blocks; force close must cancel it and
	// reclaim the borrowed resource without either being released.
	gate2 := make(chan struct{})
	h.setGate(gate2)
	done := make(chan struct{})
	go func() {
		_, _ = p.Acquire(context.Background())
		close(done)
	}()
	waitFor(t, time.Second, func() bool { return p.Stats().Creating == 1 }, "second create to start")

	if err := p.CloseForce(); err != nil {
		t.Fatalf("CloseForce: %v", err)
	}

	waitFor(t, time.Second, func() bool {
		s := p.Stats()
		live, _, _, _ := h.snapshot()
		return s.InUse == 0 && s.Creating == 0 && live == 0
	}, "forced shutdown to reclaim everything")

	if err := hd.Release(); !errors.Is(err, pool.ErrClosed) {
		t.Fatalf("Release after CloseForce err=%v, want ErrClosed", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("in-flight Acquire did not return after CloseForce")
	}
	close(gate2) // harmless: factory already exited via ctx cancellation
}

// Under mixed success/failure load every created resource is eventually
// destroyed and the quota never exceeds MaxOpen.
func TestStress(t *testing.T) {
	var counter int64
	h := &harness{}
	origCreate := h.create
	_ = origCreate

	p := pool.New(pool.Config{
		MaxOpen: 4,
		Create: func(ctx context.Context) (any, error) {
			n := atomic.AddInt64(&counter, 1)
			if n%7 == 0 {
				return nil, errors.New("injected failure")
			}
			return h.create(ctx)
		},
		Close: h.closeRes,
	})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				hd, err := p.Acquire(context.Background())
				if err != nil {
					continue
				}
				if atomic.AddInt64(&counter, 1)%29 == 0 {
					hd.MarkBad()
				}
				_ = hd.Release()
			}
		}()
	}
	wg.Wait()

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	live, peak, created, closed := h.snapshot()
	if live != 0 {
		t.Fatalf("leaked %d resources", live)
	}
	if created != closed {
		t.Fatalf("created=%d closed=%d", created, closed)
	}
	if peak > 4 {
		t.Fatalf("peak live=%d exceeds max 4", peak)
	}
}
