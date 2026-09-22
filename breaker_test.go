package groupbreaker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock is an injectable Clock whose timers fire only when the test
// advances time or explicitly triggers them.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock   *fakeClock
	at      time.Time
	f       func()
	stopped bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves time forward and fires every due, unstopped timer.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.at.After(c.now) {
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.fire()
	}
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

// fire invokes the callback unconditionally; tests use it to simulate
// stale or duplicate callback delivery.
func (t *fakeTimer) fire() { t.f() }

func (c *fakeClock) timer(i int) *fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timers[i]
}

func newTestBreaker(c *fakeClock, mutate func(*Options)) *Breaker {
	opts := Options{
		FailureThreshold:  2,
		OpenTimeout:       10 * time.Second,
		HalfOpenMaxProbes: 1,
		Clock:             c,
	}
	if mutate != nil {
		mutate(&opts)
	}
	return New(opts)
}

var errTask = errors.New("task failed")

func failNTimes(t *testing.T, b *Breaker, group string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := b.Do(group, func() error { return errTask }); !errors.Is(err, errTask) {
			t.Fatalf("call %d: got %v, want task error", i, err)
		}
	}
}

func TestTripsOpenAfterThresholdAndRejectsFast(t *testing.T) {
	clock := newFakeClock()
	b := newTestBreaker(clock, nil)

	failNTimes(t, b, "A", 1)
	if got := b.State("A"); got != Closed {
		t.Fatalf("after 1 failure: state = %v, want closed", got)
	}

	failNTimes(t, b, "A", 1)
	if got := b.State("A"); got != Open {
		t.Fatalf("after threshold: state = %v, want open", got)
	}

	ran := false
	err := b.Do("A", func() error { ran = true; return nil })
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("open call: err = %v, want ErrOpen", err)
	}
	if ran {
		t.Fatal("open circuit must reject without running the task")
	}
}

func TestExpiryMovesToHalfOpenAndProbeSuccessCloses(t *testing.T) {
	clock := newFakeClock()
	b := newTestBreaker(clock, nil)

	failNTimes(t, b, "A", 2)
	clock.Advance(10 * time.Second)
	if got := b.State("A"); got != HalfOpen {
		t.Fatalf("after timeout: state = %v, want half-open", got)
	}

	if err := b.Do("A", func() error { return nil }); err != nil {
		t.Fatalf("probe: err = %v, want nil", err)
	}
	if got := b.State("A"); got != Closed {
		t.Fatalf("after successful probe: state = %v, want closed", got)
	}
}

func TestHalfOpenProbeQuotaUnderConcurrency(t *testing.T) {
	clock := newFakeClock()
	b := newTestBreaker(clock, func(o *Options) { o.HalfOpenMaxProbes = 2 })

	failNTimes(t, b, "A", 2)
	clock.Advance(10 * time.Second)

	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Do("A", func() error {
				started <- struct{}{}
				<-release
				return nil
			}); err != nil {
				t.Errorf("admitted probe: err = %v", err)
			}
		}()
	}
	<-started
	<-started

	// Quota is exhausted: a third concurrent call must fail fast.
	ran := false
	err := b.Do("A", func() error { ran = true; return nil })
	if !errors.Is(err, ErrProbeLimit) {
		t.Fatalf("over-quota call: err = %v, want ErrProbeLimit", err)
	}
	if ran {
		t.Fatal("over-quota call must not run the task")
	}

	close(release)
	wg.Wait()
	if got := b.State("A"); got != Closed {
		t.Fatalf("after probes succeed: state = %v, want closed", got)
	}
}

func TestStaleProbeFailureCannotReopenRecoveredGeneration(t *testing.T) {
	clock := newFakeClock()
	b := newTestBreaker(clock, func(o *Options) { o.HalfOpenMaxProbes = 2 })

	failNTimes(t, b, "A", 2)
	clock.Advance(10 * time.Second) // half-open, generation G

	// Probe B becomes an in-flight probe of generation G.
	admittedB := make(chan struct{})
	releaseB := make(chan struct{})
	bDone := make(chan error, 1)
	go func() {
		bDone <- b.Do("A", func() error {
			close(admittedB)
			<-releaseB
			return errTask // stale failure from generation G
		})
	}()
	<-admittedB

	// A second probe of generation G succeeds and closes the circuit,
	// starting a new generation while B is still running.
	if err := b.Do("A", func() error { return nil }); err != nil {
		t.Fatalf("second probe: err = %v, want nil", err)
	}
	if got := b.State("A"); got != Closed {
		t.Fatalf("after first probe success: state = %v, want closed", got)
	}

	close(releaseB)
	if err := <-bDone; !errors.Is(err, errTask) {
		t.Fatalf("stale probe: err = %v, want task error", err)
	}
	if got := b.State("A"); got != Closed {
		t.Fatalf("stale failure reopened the circuit: state = %v, want closed", got)
	}
}

func TestStaleExpiryCallbackCannotMutateNewGeneration(t *testing.T) {
	clock := newFakeClock()
	b := newTestBreaker(clock, nil)

	failNTimes(t, b, "A", 2) // open gen 2, timer[0] armed for gen 2
	stale := clock.timer(0)

	clock.Advance(10 * time.Second) // timer[0] fires: half-open gen 3
	if got := b.State("A"); got != HalfOpen {
		t.Fatalf("state = %v, want half-open", got)
	}

	// Duplicate (stale) delivery of the same callback must be a no-op.
	stale.fire()
	if got := b.State("A"); got != HalfOpen {
		t.Fatalf("stale callback changed state: %v, want half-open", got)
	}

	// Failed probe reopens with a new generation and a new timer.
	if err := b.Do("A", func() error { return errTask }); !errors.Is(err, errTask) {
		t.Fatalf("probe: err = %v, want task error", err)
	}
	if got := b.State("A"); got != Open {
		t.Fatalf("after failed probe: state = %v, want open", got)
	}

	// The old callback firing now must not move the new open generation.
	stale.fire()
	if got := b.State("A"); got != Open {
		t.Fatalf("stale callback mutated new generation: %v, want open", got)
	}

	// The fresh timer still works.
	clock.Advance(10 * time.Second)
	if got := b.State("A"); got != HalfOpen {
		t.Fatalf("after fresh timeout: state = %v, want half-open", got)
	}
}

func TestGroupsAreIndependent(t *testing.T) {
	clock := newFakeClock()
	b := newTestBreaker(clock, nil)

	failNTimes(t, b, "A", 2)
	if got := b.State("A"); got != Open {
		t.Fatalf("group A: state = %v, want open", got)
	}
	if got := b.State("B"); got != Closed {
		t.Fatalf("group B: state = %v, want closed", got)
	}
	if err := b.Do("B", func() error { return nil }); err != nil {
		t.Fatalf("group B call: err = %v, want nil", err)
	}
	if err := b.Do("A", func() error { return nil }); !errors.Is(err, ErrOpen) {
		t.Fatalf("group A call: err = %v, want ErrOpen", err)
	}
}

func TestFailureClassifierIsInjectable(t *testing.T) {
	clock := newFakeClock()
	errIgnorable := errors.New("ignorable")
	b := newTestBreaker(clock, func(o *Options) {
		o.IsFailure = func(err error) bool { return !errors.Is(err, errIgnorable) }
	})

	for i := 0; i < 5; i++ {
		if err := b.Do("A", func() error { return errIgnorable }); !errors.Is(err, errIgnorable) {
			t.Fatalf("call %d: err = %v", i, err)
		}
	}
	if got := b.State("A"); got != Closed {
		t.Fatalf("ignorable errors tripped the circuit: %v", got)
	}

	failNTimes(t, b, "A", 2)
	if got := b.State("A"); got != Open {
		t.Fatalf("real failures did not trip the circuit: %v", got)
	}
}

func TestConcurrentMixedLoad(t *testing.T) {
	clock := newFakeClock()
	b := newTestBreaker(clock, func(o *Options) { o.HalfOpenMaxProbes = 3 })

	groups := []string{"A", "B", "C", "D"}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g := groups[i%len(groups)]
			for j := 0; j < 50; j++ {
				_ = b.Do(g, func() error {
					if j%3 == 0 {
						return errTask
					}
					return nil
				})
				if j == 25 {
					clock.Advance(10 * time.Second)
				}
			}
		}(i)
	}
	wg.Wait()

	for _, g := range groups {
		switch b.State(g) {
		case Closed, Open, HalfOpen:
		default:
			t.Fatalf("group %s in invalid state", g)
		}
	}
}
