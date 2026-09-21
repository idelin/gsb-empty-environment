package breaker_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"breaker"
)

var errTask = errors.New("task failed")

// fakeTime implements both breaker.Clock and breaker.Scheduler so tests can
// inject time and drive expiry callbacks deterministically.
type fakeTime struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
}

type fakeTimer struct {
	due time.Time
	fn  func()
}

func newFakeTime(start time.Time) *fakeTime { return &fakeTime{now: start} }

func (f *fakeTime) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeTime) AfterFunc(d time.Duration, fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timers = append(f.timers, fakeTimer{due: f.now.Add(d), fn: fn})
}

// Advance moves the clock forward and fires every due timer outside the lock.
func (f *fakeTime) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	var due []func()
	keep := f.timers[:0]
	for _, t := range f.timers {
		if t.due.After(f.now) {
			keep = append(keep, t)
		} else {
			due = append(due, t.fn)
		}
	}
	f.timers = keep
	f.mu.Unlock()
	for _, fn := range due {
		fn()
	}
}

// pop removes and returns the earliest pending timer callback without
// running it, so a test can fire it manually (e.g. as a stale duplicate).
func (f *fakeTime) pop() func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.timers) == 0 {
		return nil
	}
	fn := f.timers[0].fn
	f.timers = f.timers[1:]
	return fn
}

func newManager(ft *fakeTime, threshold int, open time.Duration, probes int) *breaker.Manager {
	return breaker.NewManager(breaker.Options{
		FailureThreshold:  threshold,
		OpenDuration:      open,
		HalfOpenMaxProbes: probes,
		Clock:             ft,
		Scheduler:         ft,
	})
}

func failTask() error { return errTask }
func okTask() error   { return nil }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestOpenAfterThresholdAndFastFail(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 3, time.Minute, 1)

	ran := 0
	task := func() error { ran++; return errTask }
	for i := 0; i < 3; i++ {
		if err := m.Execute("g", task); !errors.Is(err, errTask) {
			t.Fatalf("call %d: got %v, want %v", i, err, errTask)
		}
	}
	if got := m.State("g"); got != breaker.Open {
		t.Fatalf("state = %v, want open", got)
	}
	if err := m.Execute("g", task); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("open call: got %v, want ErrOpen", err)
	}
	if ran != 3 {
		t.Fatalf("task ran %d times, want 3 (open circuit must fail fast)", ran)
	}
}

func TestOpenExpiresToHalfOpen(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 1, time.Minute, 1)

	m.Execute("g", failTask)
	if got := m.State("g"); got != breaker.Open {
		t.Fatalf("state = %v, want open", got)
	}
	ft.Advance(time.Minute)
	if got := m.State("g"); got != breaker.HalfOpen {
		t.Fatalf("state = %v, want half-open after expiry", got)
	}
}

func TestHalfOpenProbeQuota(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 1, time.Minute, 2)

	m.Execute("g", failTask)
	ft.Advance(time.Minute) // -> half-open

	entered := make(chan struct{}, 2)
	proceed := make(chan error, 2)
	task := func() error {
		entered <- struct{}{}
		return <-proceed
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Execute("g", task)
		}()
	}
	<-entered
	<-entered // both probes admitted concurrently

	// A third concurrent request exceeds the probe quota.
	if err := m.Execute("g", okTask); !errors.Is(err, breaker.ErrProbeLimit) {
		t.Fatalf("over-quota call: got %v, want ErrProbeLimit", err)
	}

	proceed <- nil
	proceed <- nil
	wg.Wait()
	if got := m.State("g"); got != breaker.Closed {
		t.Fatalf("state = %v, want closed after successful probe", got)
	}
}

func TestHalfOpenProbeFailureReopens(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 1, time.Minute, 1)

	m.Execute("g", failTask)
	ft.Advance(time.Minute) // -> half-open

	if err := m.Execute("g", failTask); !errors.Is(err, errTask) {
		t.Fatalf("probe: got %v, want task error", err)
	}
	if got := m.State("g"); got != breaker.Open {
		t.Fatalf("state = %v, want open after failed probe", got)
	}
	if err := m.Execute("g", okTask); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("reopened call: got %v, want ErrOpen", err)
	}
	ft.Advance(time.Minute)
	if got := m.State("g"); got != breaker.HalfOpen {
		t.Fatalf("state = %v, want half-open after second expiry", got)
	}
}

// A probe started in an old generation must not reopen a newer generation
// that has already recovered to half-open.
func TestStaleProbeResultIgnored(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 1, time.Minute, 2)

	m.Execute("g", failTask)
	ft.Advance(time.Minute) // -> half-open, generation G1

	entered := make(chan struct{}, 2)
	proceed := make(chan error, 2)
	task := func() error {
		entered <- struct{}{}
		return <-proceed
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Execute("g", task)
		}()
	}
	<-entered
	<-entered // two G1 probes in flight

	proceed <- errTask // first probe fails -> reopen, generation G2
	waitFor(t, "reopen", func() bool { return m.State("g") == breaker.Open })

	ft.Advance(time.Minute) // -> half-open again, generation G3
	if got := m.State("g"); got != breaker.HalfOpen {
		t.Fatalf("state = %v, want half-open", got)
	}

	proceed <- errTask // stale G1 failure arrives late
	wg.Wait()
	if got := m.State("g"); got != breaker.HalfOpen {
		t.Fatalf("stale probe reopened the group: state = %v, want half-open", got)
	}

	// The new generation is still usable: a fresh probe closes the circuit.
	if err := m.Execute("g", okTask); err != nil {
		t.Fatalf("fresh probe: got %v, want nil", err)
	}
	if got := m.State("g"); got != breaker.Closed {
		t.Fatalf("state = %v, want closed", got)
	}
}

// An expiry callback from an old generation must not move a newer
// generation out of the open state.
func TestStaleExpiryCallbackIgnored(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 1, time.Minute, 1)

	m.Execute("g", failTask) // -> open, generation G1, schedules cb1
	cb1 := ft.pop()
	if cb1 == nil {
		t.Fatal("no expiry callback scheduled")
	}

	cb1() // -> half-open, generation G2
	if got := m.State("g"); got != breaker.HalfOpen {
		t.Fatalf("state = %v, want half-open", got)
	}

	m.Execute("g", failTask) // probe fails -> open, generation G3, schedules cb2
	if got := m.State("g"); got != breaker.Open {
		t.Fatalf("state = %v, want open", got)
	}

	cb1() // stale duplicate of the G1 callback
	if got := m.State("g"); got != breaker.Open {
		t.Fatalf("stale expiry callback fired: state = %v, want open", got)
	}

	ft.Advance(time.Minute) // fires cb2, the live callback
	if got := m.State("g"); got != breaker.HalfOpen {
		t.Fatalf("state = %v, want half-open after live expiry", got)
	}
}

func TestGroupIsolation(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 1, time.Minute, 1)

	m.Execute("A", failTask) // opens group A only
	if got := m.State("A"); got != breaker.Open {
		t.Fatalf("A state = %v, want open", got)
	}
	if got := m.State("B"); got != breaker.Closed {
		t.Fatalf("B state = %v, want closed (unaffected by A)", got)
	}
	if err := m.Execute("B", okTask); err != nil {
		t.Fatalf("B call: got %v, want nil", err)
	}
	if got := m.State("B"); got != breaker.Closed {
		t.Fatalf("B state = %v, want closed", got)
	}
}

func TestClosedSuccessResetsFailures(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 2, time.Minute, 1)

	m.Execute("g", failTask) // 1 failure
	m.Execute("g", okTask)   // resets the counter
	m.Execute("g", failTask) // 1 failure again
	if got := m.State("g"); got != breaker.Closed {
		t.Fatalf("state = %v, want closed (counter was reset)", got)
	}
	m.Execute("g", failTask) // 2 consecutive -> open
	if got := m.State("g"); got != breaker.Open {
		t.Fatalf("state = %v, want open", got)
	}
}

// Hammers the manager from many goroutines while the clock advances; run
// with -race to validate the synchronisation.
func TestConcurrentRace(t *testing.T) {
	ft := newFakeTime(time.Unix(1000, 0))
	m := newManager(ft, 3, 10*time.Millisecond, 2)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			groups := []string{"g0", "g1", "g2", "g3"}
			for i := 0; i < 200; i++ {
				name := groups[(id+i)%len(groups)]
				m.Execute(name, func() error {
					if (i+id)%3 == 0 {
						return errTask
					}
					return nil
				})
				_ = m.State(name)
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			ft.Advance(5 * time.Millisecond)
		}
	}()
	wg.Wait()
}
