package hedged

import (
	"context"
	"errors"
	"hash/crc32"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- fake clock -----------------------------------------------------------

type fakeTimer struct {
	ch      chan time.Time
	stopped atomic.Bool
	fired   atomic.Bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	if t.fired.Load() {
		return false
	}
	return !t.stopped.Swap(true)
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(time.Duration) Timer {
	t := &fakeTimer{ch: make(chan time.Time, 1)}
	c.mu.Lock()
	c.timers = append(c.timers, t)
	c.mu.Unlock()
	return t
}

// fire triggers every live timer, as if their deadline had arrived.
func (c *fakeClock) fire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.timers {
		if !t.stopped.Load() && t.fired.CompareAndSwap(false, true) {
			t.ch <- c.now
		}
	}
}

func (c *fakeClock) timerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func (c *fakeClock) liveTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.stopped.Load() && !t.fired.Load() {
			n++
		}
	}
	return n
}

// --- helpers ---------------------------------------------------------------

func mkResult(s string, ver uint64) Result {
	b := []byte(s)
	return Result{Value: b, Version: ver, Checksum: crc32.ChecksumIEEE(b)}
}

func waitTimer(t *testing.T, c *fakeClock) {
	t.Helper()
	waitFor(t, func() bool { return c.timerCount() >= 1 }, "hedge timer created")
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

// --- tests -----------------------------------------------------------------

// A fast valid primary wins; the hedge timer is stopped and the secondary
// is never started, even if the clock is fired afterwards.
func TestPrimaryWinsBeforeHedge(t *testing.T) {
	clock := newFakeClock()
	var secondaryCalls atomic.Int32
	f, err := New(Config{
		MinVersion: 1,
		HedgeDelay: time.Minute,
		Clock:      clock,
		Primary:    func(context.Context) (Result, error) { return mkResult("primary", 1), nil },
		Secondary: func(context.Context) (Result, error) {
			secondaryCalls.Add(1)
			return mkResult("secondary", 1), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	res, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Value); got != "primary" {
		t.Fatalf("winner = %q, want primary", got)
	}
	// Timer vs completion race: the hedge timer must be stopped on win.
	waitFor(t, func() bool { return clock.liveTimers() == 0 }, "hedge timer stopped")
	clock.fire() // must not start the secondary retroactively
	if n := secondaryCalls.Load(); n != 0 {
		t.Fatalf("secondary started %d times, want 0", n)
	}
}

// A result that arrives first but is below MinVersion cannot win; the
// fetcher waits for a legal candidate.
func TestStaleFirstArrivalCannotWin(t *testing.T) {
	clock := newFakeClock()
	release := make(chan struct{})
	f, err := New(Config{
		MinVersion: 2,
		HedgeDelay: time.Minute,
		Clock:      clock,
		Primary:    func(context.Context) (Result, error) { return mkResult("stale", 1), nil },
		Secondary: func(ctx context.Context) (Result, error) {
			select {
			case <-release:
				return mkResult("fresh", 2), nil
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := f.Fetch(context.Background())
		done <- out{res, err}
	}()

	// The stale primary must not win; the failure triggers the hedge.
	time.Sleep(20 * time.Millisecond)
	select {
	case o := <-done:
		t.Fatalf("stale result won: %+v", o.res)
	default:
	}
	close(release)
	o := <-done
	if o.err != nil {
		t.Fatal(o.err)
	}
	if got := string(o.res.Value); got != "fresh" {
		t.Fatalf("winner = %q, want fresh", got)
	}
	if o.res.Version != 2 {
		t.Fatalf("version = %d, want 2", o.res.Version)
	}
}

// A checksum mismatch is treated as failure, even with a high version.
// Uses a custom injected checksum function.
func TestChecksumMismatchRejected(t *testing.T) {
	clock := newFakeClock()
	sum := func(b []byte) uint32 { return uint32(len(b)) * 7 }
	corrupt := Result{Value: []byte("tampered"), Version: 99, Checksum: 1}
	good := []byte("good")
	f, err := New(Config{
		MinVersion: 1,
		HedgeDelay: time.Minute,
		Clock:      clock,
		Checksum:   sum,
		Primary:    func(context.Context) (Result, error) { return corrupt, nil },
		Secondary: func(context.Context) (Result, error) {
			return Result{Value: good, Version: 1, Checksum: sum(good)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	res, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Value); got != "good" {
		t.Fatalf("winner = %q, want good", got)
	}
}

// On a cache miss the secondary is started only after the hedge delay.
func TestHedgeStartsAfterDelay(t *testing.T) {
	clock := newFakeClock()
	primaryCancelled := make(chan struct{})
	secondaryStarted := make(chan struct{})
	f, err := New(Config{
		MinVersion: 1,
		HedgeDelay: time.Minute,
		Clock:      clock,
		Primary: func(ctx context.Context) (Result, error) {
			<-ctx.Done()
			close(primaryCancelled)
			return Result{}, ctx.Err()
		},
		Secondary: func(context.Context) (Result, error) {
			close(secondaryStarted)
			return mkResult("hedged", 1), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	done := make(chan Result, 1)
	go func() {
		res, _ := f.Fetch(context.Background())
		done <- res
	}()

	waitTimer(t, clock)
	select {
	case <-secondaryStarted:
		t.Fatal("secondary started before the hedge delay elapsed")
	case <-time.After(20 * time.Millisecond):
	}
	clock.fire() // hedge delay elapses
	select {
	case <-secondaryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("secondary did not start after the hedge delay")
	}
	res := <-done
	if got := string(res.Value); got != "hedged" {
		t.Fatalf("winner = %q, want hedged", got)
	}
	<-primaryCancelled // losing candidate was cancelled
}

// When both sources fail, the error aggregates both reasons.
func TestBothFailAggregatesReasons(t *testing.T) {
	clock := newFakeClock()
	f, err := New(Config{
		MinVersion: 1,
		HedgeDelay: time.Minute,
		Clock:      clock,
		Primary:    func(context.Context) (Result, error) { return Result{}, errors.New("connection refused") },
		Secondary:  func(context.Context) (Result, error) { return Result{}, errors.New("deadline exceeded") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	_, err = f.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection refused") ||
		!strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("error %q does not aggregate both reasons", err)
	}
	if !strings.Contains(err.Error(), "primary") || !strings.Contains(err.Error(), "secondary") {
		t.Fatalf("error %q does not name both sources", err)
	}
}

// After a winner is decided, a late result must not overwrite it, and the
// losing candidate is cancelled.
func TestLateResultDoesNotOverwrite(t *testing.T) {
	clock := newFakeClock()
	secondaryCancelled := make(chan struct{})
	f, err := New(Config{
		MinVersion: 1,
		HedgeDelay: time.Minute,
		Clock:      clock,
		Primary: func(ctx context.Context) (Result, error) {
			// Slow enough that the hedge fires first, but valid.
			select {
			case <-time.After(50 * time.Millisecond):
				return mkResult("winner", 3), nil
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		},
		Secondary: func(ctx context.Context) (Result, error) {
			<-ctx.Done()
			close(secondaryCancelled)
			return mkResult("late-loser", 5), nil // late, must be discarded
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := f.Fetch(context.Background())
		done <- out{res, err}
	}()
	waitTimer(t, clock)
	clock.fire() // start the hedge before the primary answers

	o := <-done
	if o.err != nil {
		t.Fatal(o.err)
	}
	if got := string(o.res.Value); got != "winner" || o.res.Version != 3 {
		t.Fatalf("winner = %q v%d, want winner v3 (late result must not overwrite)", o.res.Value, o.res.Version)
	}
	<-secondaryCancelled // losing candidate was cancelled
}

// The global speculative budget bounds concurrent hedged requests.
func TestSpeculativeBudgetLimit(t *testing.T) {
	clock := newFakeClock()
	var secondaryCalls atomic.Int32
	primaryBlock := make(chan struct{})
	secondaryStarted := make(chan struct{})
	secondaryRelease := make(chan struct{})

	f, err := New(Config{
		MinVersion:     1,
		HedgeDelay:     time.Minute,
		MaxSpeculative: 1,
		Clock:          clock,
		Primary: func(ctx context.Context) (Result, error) {
			select {
			case <-primaryBlock:
				return Result{}, errors.New("primary unavailable")
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		},
		Secondary: func(ctx context.Context) (Result, error) {
			secondaryCalls.Add(1)
			close(secondaryStarted)
			select {
			case <-secondaryRelease:
				return mkResult("hedged", 1), nil
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Fetch A: primary blocks, hedge fires and consumes the whole budget.
	type out struct {
		res Result
		err error
	}
	aDone := make(chan out, 1)
	go func() {
		res, err := f.Fetch(context.Background())
		aDone <- out{res, err}
	}()
	waitTimer(t, clock)
	clock.fire()
	select {
	case <-secondaryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("fetch A did not hedge")
	}

	// Unblock the primaries: both A's and B's primaries now fail at once.
	close(primaryBlock)

	// Fetch B: primary fails immediately, but the budget is exhausted, so
	// no speculative request may be started for it.
	_, errB := f.Fetch(context.Background())
	if errB == nil || !strings.Contains(errB.Error(), "primary unavailable") {
		t.Fatalf("fetch B error = %v, want primary failure", errB)
	}
	if n := secondaryCalls.Load(); n != 1 {
		t.Fatalf("secondary started %d times, want 1 (budget = 1)", n)
	}

	close(secondaryRelease)
	a := <-aDone
	if a.err != nil {
		t.Fatal(a.err)
	}
	if got := string(a.res.Value); got != "hedged" {
		t.Fatalf("fetch A winner = %q, want hedged", got)
	}
}

// Close cancels in-flight fetches, stops timers, waits for goroutines, and
// makes subsequent Fetch calls fail with ErrClosed.
func TestCloseReleasesAllResources(t *testing.T) {
	clock := newFakeClock()
	primaryStarted := make(chan struct{})
	f, err := New(Config{
		MinVersion: 1,
		HedgeDelay: time.Minute,
		Clock:      clock,
		Primary: func(ctx context.Context) (Result, error) {
			close(primaryStarted)
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
		Secondary: func(ctx context.Context) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	baseline := runtime.NumGoroutine()
	fetchErr := make(chan error, 1)
	go func() {
		_, err := f.Fetch(context.Background())
		fetchErr <- err
	}()
	<-primaryStarted
	if n := clock.liveTimers(); n != 1 {
		t.Fatalf("live timers = %d, want 1", n)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-fetchErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight Fetch error = %v, want context.Canceled", err)
	}
	waitFor(t, func() bool { return runtime.NumGoroutine() <= baseline }, "goroutines released")
	if n := clock.liveTimers(); n != 0 {
		t.Fatalf("live timers after Close = %d, want 0", n)
	}
	if _, err := f.Fetch(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Fetch after Close = %v, want ErrClosed", err)
	}
	if err := f.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
}
