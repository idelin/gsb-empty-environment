package hedged

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// manualClock is an injectable Clock whose timers only fire when told to.
type manualClock struct {
	mu     sync.Mutex
	timers []*manualTimer
}

type manualTimer struct {
	f       func()
	fired   bool
	stopped bool
}

func (c *manualClock) AfterFunc(_ time.Duration, f func()) func() bool {
	t := &manualTimer{f: f}
	c.mu.Lock()
	c.timers = append(c.timers, t)
	c.mu.Unlock()
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if t.fired || t.stopped {
			return false
		}
		t.stopped = true
		return true
	}
}

// pending reports how many timers are registered but not yet fired/stopped.
func (c *manualClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.fired && !t.stopped {
			n++
		}
	}
	return n
}

// fire runs all pending timer callbacks (simulating the delay elapsing).
func (c *manualClock) fire() {
	c.mu.Lock()
	var fs []func()
	for _, t := range c.timers {
		if !t.fired && !t.stopped {
			t.fired = true
			fs = append(fs, t.f)
		}
	}
	c.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

// gate is a controllable source: it blocks until released or ctx is canceled.
type gate struct {
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newGate() *gate {
	return &gate{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
}

func (g *gate) source(res Result, err error) Source {
	return func(ctx context.Context, _ string) (Result, error) {
		g.once.Do(func() { close(g.started) })
		select {
		case <-g.release:
			return res, err
		case <-ctx.Done():
			g.once.Do(func() {}) // started already closed
			select {
			case <-g.canceled:
			default:
				close(g.canceled)
			}
			return Result{}, ctx.Err()
		}
	}
}

func waitCh(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func getAsync(f *Fetcher, ctx context.Context, key string) (<-chan Result, <-chan error) {
	resCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := f.Get(ctx, key)
		if err != nil {
			errCh <- err
			return
		}
		resCh <- r
	}()
	return resCh, errCh
}

func TestCacheHitSkipsSources(t *testing.T) {
	var calls atomic.Int32
	src := func(context.Context, string) (Result, error) {
		calls.Add(1)
		return NewResult([]byte("v"), 1), nil
	}
	f := NewFetcher(Config{
		Primary: src, Hedge: src, Clock: RealClock{},
		HedgeDelay: time.Hour, MinVersion: 1, SpeculativeQuota: 1,
	})
	defer f.Close()

	for i := 0; i < 3; i++ {
		got, err := f.Get(context.Background(), "k")
		if err != nil || string(got.Value) != "v" {
			t.Fatalf("Get = %q, %v", got.Value, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("source called %d times, want 1 (cache miss only)", n)
	}
}

func TestHedgeStartsAfterDelayAndCancelsLoser(t *testing.T) {
	clock := &manualClock{}
	primary := newGate()
	hedge := newGate()
	var hedgeCalls atomic.Int32
	hedgeSrc := func(ctx context.Context, k string) (Result, error) {
		hedgeCalls.Add(1)
		return hedge.source(NewResult([]byte("hedge"), 2), nil)(ctx, k)
	}
	f := NewFetcher(Config{
		Primary: primary.source(NewResult([]byte("primary"), 1), nil),
		Hedge:   hedgeSrc, Clock: clock,
		HedgeDelay: time.Minute, MinVersion: 1, SpeculativeQuota: 1,
	})
	defer f.Close()

	resCh, errCh := getAsync(f, context.Background(), "k")
	waitCh(t, primary.started, "primary start")

	// Hedge must NOT start before the delay elapses.
	if n := hedgeCalls.Load(); n != 0 {
		t.Fatalf("hedge started before delay: %d calls", n)
	}

	clock.fire() // delay elapses
	waitCh(t, hedge.started, "hedge start after delay")
	close(hedge.release)

	select {
	case r := <-resCh:
		if string(r.Value) != "hedge" || r.Version != 2 {
			t.Fatalf("winner = %q v%d, want hedge v2", r.Value, r.Version)
		}
	case err := <-errCh:
		t.Fatalf("Get failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return")
	}

	// The loser (primary) must have been canceled.
	waitCh(t, primary.canceled, "primary cancellation")
}

func TestStaleEarlyResultCannotWin(t *testing.T) {
	clock := &manualClock{}
	primary := newGate() // slow, returns the fresh version
	f := NewFetcher(Config{
		Primary: primary.source(NewResult([]byte("fresh"), 7), nil),
		Hedge: func(context.Context, string) (Result, error) {
			return NewResult([]byte("stale"), 3), nil // fast but too old
		},
		Clock: clock, HedgeDelay: time.Minute, MinVersion: 5, SpeculativeQuota: 1,
	})
	defer f.Close()

	resCh, errCh := getAsync(f, context.Background(), "k")
	waitCh(t, primary.started, "primary start")
	clock.fire() // hedge starts and immediately returns a stale result

	// Give the loop a moment: the stale result must NOT win.
	select {
	case r := <-resCh:
		t.Fatalf("stale result won: %q v%d", r.Value, r.Version)
	case err := <-errCh:
		t.Fatalf("Get failed early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(primary.release)
	select {
	case r := <-resCh:
		if r.Version != 7 || string(r.Value) != "fresh" {
			t.Fatalf("winner = %q v%d, want fresh v7", r.Value, r.Version)
		}
	case err := <-errCh:
		t.Fatalf("Get failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return")
	}
}

func TestBothFailAggregatesReasons(t *testing.T) {
	errPrimary := errors.New("primary exploded")
	errHedge := errors.New("hedge unavailable")
	f := NewFetcher(Config{
		Primary: func(context.Context, string) (Result, error) { return Result{}, errPrimary },
		Hedge:   func(context.Context, string) (Result, error) { return Result{}, errHedge },
		Clock:   RealClock{}, HedgeDelay: time.Hour, MinVersion: 1,
	})
	defer f.Close()

	_, err := f.Get(context.Background(), "k")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errPrimary) || !errors.Is(err, errHedge) {
		t.Fatalf("error %v does not aggregate both causes", err)
	}
	if !strings.Contains(err.Error(), "source 0") || !strings.Contains(err.Error(), "source 1") {
		t.Fatalf("error %q missing source labels", err)
	}
}

func TestLateResultDoesNotOverwrite(t *testing.T) {
	clock := &manualClock{}
	primary := newGate()
	f := NewFetcher(Config{
		Primary: primary.source(NewResult([]byte("primary-late"), 9), nil),
		Hedge: func(context.Context, string) (Result, error) {
			return NewResult([]byte("hedge-fast"), 1), nil
		},
		Clock: clock, HedgeDelay: time.Minute, MinVersion: 1, SpeculativeQuota: 1,
	})
	defer f.Close()

	resCh, errCh := getAsync(f, context.Background(), "k")
	waitCh(t, primary.started, "primary start")
	clock.fire()

	select {
	case r := <-resCh:
		if string(r.Value) != "hedge-fast" {
			t.Fatalf("winner = %q, want hedge-fast", r.Value)
		}
	case err := <-errCh:
		t.Fatalf("Get failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return")
	}

	// The primary's late (and newer!) result arrives after the winner.
	close(primary.release)
	time.Sleep(50 * time.Millisecond)

	got, err := f.Get(context.Background(), "k")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if string(got.Value) != "hedge-fast" {
		t.Fatalf("late result overwrote winner: got %q", got.Value)
	}
}

func TestSpeculativeQuotaIsGlobal(t *testing.T) {
	clock := &manualClock{}
	g1, g2 := newGate(), newGate()
	primary := func(ctx context.Context, key string) (Result, error) {
		if key == "k1" {
			return g1.source(NewResult([]byte("p1"), 1), nil)(ctx, key)
		}
		return g2.source(NewResult([]byte("p2"), 1), nil)(ctx, key)
	}
	var hedgeCalls atomic.Int32
	hedge := func(ctx context.Context, key string) (Result, error) {
		hedgeCalls.Add(1)
		<-ctx.Done() // block until canceled
		return Result{}, ctx.Err()
	}
	f := NewFetcher(Config{
		Primary: primary, Hedge: hedge, Clock: clock,
		HedgeDelay: time.Minute, MinVersion: 1, SpeculativeQuota: 1, // global budget: 1
	})
	defer f.Close()

	resCh1, errCh1 := getAsync(f, context.Background(), "k1")
	resCh2, errCh2 := getAsync(f, context.Background(), "k2")
	waitCh(t, g1.started, "primary k1 start")
	waitCh(t, g2.started, "primary k2 start")

	clock.fire() // both hedge timers fire; only one may acquire the quota
	time.Sleep(50 * time.Millisecond)
	if n := hedgeCalls.Load(); n != 1 {
		t.Fatalf("hedge started %d times, want exactly 1 (quota)", n)
	}

	// Both primaries eventually succeed.
	close(g1.release)
	close(g2.release)
	for i, ch := range []<-chan Result{resCh1, resCh2} {
		select {
		case <-ch:
		case err := <-[]<-chan error{errCh1, errCh2}[i]:
			t.Fatalf("Get %d failed: %v", i, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("Get %d did not return", i)
		}
	}
	if n := hedgeCalls.Load(); n != 1 {
		t.Fatalf("hedge started %d times after completion, want 1", n)
	}
}

func TestTimerStoppedAfterWinner(t *testing.T) {
	for i := 0; i < 50; i++ {
		clock := &manualClock{}
		var hedgeCalls atomic.Int32
		f := NewFetcher(Config{
			Primary: func(context.Context, string) (Result, error) {
				return NewResult([]byte("primary"), 1), nil
			},
			Hedge: func(ctx context.Context, _ string) (Result, error) {
				hedgeCalls.Add(1)
				<-ctx.Done()
				return Result{}, ctx.Err()
			},
			Clock: clock, HedgeDelay: time.Minute, MinVersion: 1, SpeculativeQuota: 1,
		})
		if _, err := f.Get(context.Background(), "k"); err != nil {
			t.Fatalf("Get: %v", err)
		}
		clock.fire() // timer must have been stopped by the winner
		if n := hedgeCalls.Load(); n != 0 {
			t.Fatalf("iter %d: hedge started after winner: %d calls", i, n)
		}
		f.Close()
	}
}

func TestTimerFiresHedgeExactlyOnce(t *testing.T) {
	clock := &manualClock{}
	primary := newGate()
	var hedgeCalls atomic.Int32
	f := NewFetcher(Config{
		Primary: primary.source(NewResult([]byte("p"), 1), nil),
		Hedge: func(ctx context.Context, _ string) (Result, error) {
			hedgeCalls.Add(1)
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
		Clock: clock, HedgeDelay: time.Minute, MinVersion: 1, SpeculativeQuota: 4,
	})
	defer f.Close()

	resCh, errCh := getAsync(f, context.Background(), "k")
	waitCh(t, primary.started, "primary start")
	clock.fire()
	clock.fire()
	clock.fire()
	time.Sleep(50 * time.Millisecond)
	if n := hedgeCalls.Load(); n != 1 {
		t.Fatalf("hedge started %d times, want exactly 1", n)
	}

	// Let the primary win so the fetch completes and the hedge is canceled.
	close(primary.release)
	select {
	case <-resCh:
	case err := <-errCh:
		t.Fatalf("Get failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return")
	}
}

func TestTimerCompletionRaceNoExtraRequests(t *testing.T) {
	// Winner completes while the timer callback is racing to fire: the hedge
	// must never start once the winner is decided.
	for i := 0; i < 200; i++ {
		clock := &manualClock{}
		primary := newGate()
		var hedgeCalls atomic.Int32
		f := NewFetcher(Config{
			Primary: primary.source(NewResult([]byte("p"), 1), nil),
			Hedge: func(ctx context.Context, _ string) (Result, error) {
				hedgeCalls.Add(1)
				<-ctx.Done()
				return Result{}, ctx.Err()
			},
			Clock: clock, HedgeDelay: time.Minute, MinVersion: 1, SpeculativeQuota: 1,
		})
		resCh, errCh := getAsync(f, context.Background(), "k")
		waitCh(t, primary.started, "primary start")
		// Race: release the primary and fire the timer concurrently.
		go clock.fire()
		close(primary.release)
		select {
		case <-resCh:
		case err := <-errCh:
			t.Fatalf("iter %d: Get failed: %v", i, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: Get did not return", i)
		}
		// After the winner is decided, fire again (possibly concurrently
		// with the in-flight first fire) and check no hedge ever started
		// once Get returned.
		clock.fire()
		f.Close()
		if n := hedgeCalls.Load(); n > 1 {
			t.Fatalf("iter %d: hedge started %d times", i, n)
		}
	}
}

func TestCloseReleasesAllResources(t *testing.T) {
	clock := &manualClock{}
	var active atomic.Int32
	blocking := func(released *atomic.Int32) Source {
		return func(ctx context.Context, _ string) (Result, error) {
			active.Add(1)
			defer active.Add(-1)
			defer released.Add(1)
			<-ctx.Done()
			return Result{}, ctx.Err()
		}
	}
	var relPrimary, relHedge atomic.Int32
	f := NewFetcher(Config{
		Primary: blocking(&relPrimary), Hedge: blocking(&relHedge),
		Clock: clock, HedgeDelay: time.Minute, MinVersion: 1, SpeculativeQuota: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	_, errCh := getAsync(f, ctx, "k")
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitFor("hedge timer armed", func() bool { return clock.pending() == 1 })
	clock.fire()
	waitFor("both sources in flight", func() bool { return active.Load() == 2 })

	cancel() // caller cancels: fetch unwinds and cancels candidates
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return after cancel")
	}

	done := make(chan struct{})
	go func() { f.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not release resources")
	}
	if n := active.Load(); n != 0 {
		t.Fatalf("%d source goroutines still active after Close", n)
	}
	if relPrimary.Load() != 1 || relHedge.Load() != 1 {
		t.Fatalf("sources not released: primary=%d hedge=%d", relPrimary.Load(), relHedge.Load())
	}
	if _, err := f.Get(context.Background(), "k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v, want ErrClosed", err)
	}
}

func TestChecksumMismatchFallsBackToHedge(t *testing.T) {
	bad := Result{Value: []byte("corrupt"), Version: 9, Checksum: 0xdeadbeef}
	f := NewFetcher(Config{
		Primary: func(context.Context, string) (Result, error) { return bad, nil },
		Hedge: func(context.Context, string) (Result, error) {
			return NewResult([]byte("good"), 9), nil
		},
		Clock: RealClock{}, HedgeDelay: time.Hour, MinVersion: 1,
	})
	defer f.Close()

	got, err := f.Get(context.Background(), "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Value) != "good" {
		t.Fatalf("winner = %q, want good (checksum-invalid primary rejected)", got.Value)
	}
}

func TestBothStaleAggregatesVersionErrors(t *testing.T) {
	f := NewFetcher(Config{
		Primary: func(context.Context, string) (Result, error) { return NewResult([]byte("a"), 1), nil },
		Hedge:   func(context.Context, string) (Result, error) { return NewResult([]byte("b"), 2), nil },
		Clock:   RealClock{}, HedgeDelay: time.Hour, MinVersion: 10,
	})
	defer f.Close()

	_, err := f.Get(context.Background(), "k")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "source 0: version 1 below minimum 10") ||
		!strings.Contains(msg, "source 1: version 2 below minimum 10") {
		t.Fatalf("unexpected aggregated error: %q", msg)
	}
}

func TestRealClockSmoke(t *testing.T) {
	primary := func(ctx context.Context, _ string) (Result, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return NewResult([]byte("primary"), 1), nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	hedge := func(context.Context, string) (Result, error) {
		return NewResult([]byte("hedge"), 1), nil
	}
	f := NewFetcher(Config{
		Primary: primary, Hedge: hedge, Clock: RealClock{},
		HedgeDelay: 20 * time.Millisecond, MinVersion: 1, SpeculativeQuota: 1,
	})
	defer f.Close()

	got, err := f.Get(context.Background(), "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Value) != "hedge" {
		t.Fatalf("winner = %q, want hedge (primary too slow)", got.Value)
	}
}
