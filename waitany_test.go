package waitany_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"waitany"
)

// ---------------------------------------------------------------------------
// Deterministic schedule enumeration
//
// WaitAny registers events sequentially, so a whole schedule can be described
// by, for each event, the registration step at which it fires (0..N, or
// never), and the step at which the context is cancelled (0..N, during the
// waiting phase, or never). Step k means "inside the Subscribe call for
// event k"; step N means "after the last Subscribe returned, before WaitAny
// blocks". The harness below plays every such schedule against the real
// WaitAny and checks the result plus the invariants:
//
//   - the result is delivered exactly once and matches the model;
//   - no subscription is left behind (every issued Subscription is
//     unsubscribed exactly once);
//   - no completed event is missed: an event whose callback took effect
//     before the cancellation was observed must win;
//   - no callback is delivered more than once per subscription.
// ---------------------------------------------------------------------------

const (
	never    = -1 // event never fires / no cancellation
	waitStep = -2 // cancellation happens while WaitAny is blocked
)

// testEvent is a synchronous, fully deterministic one-shot event source.
type testEvent struct {
	fired       bool
	pending     []func()
	subscribes  int // total Subscribe calls
	subsIssued  int // Subscribe calls that returned a non-nil Subscription
	unsubs      int // Unsubscribe calls received
	activeSubs  int // subsIssued - unsubs
	cbDelivered int // callback invocations performed by this source
}

func (e *testEvent) Subscribe(cb func()) waitany.Subscription {
	e.subscribes++
	if e.fired {
		e.cbDelivered++
		cb()
		return nil
	}
	e.pending = append(e.pending, cb)
	e.subsIssued++
	e.activeSubs++
	return testSub{ev: e}
}

func (e *testEvent) Fire() {
	if e.fired {
		panic("event fired twice")
	}
	e.fired = true
	for _, cb := range e.pending {
		e.cbDelivered++
		cb()
	}
	e.pending = nil
}

type testSub struct{ ev *testEvent }

func (s testSub) Unsubscribe() {
	s.ev.unsubs++
	s.ev.activeSubs--
}

// harness plays a schedule: it runs the hook actions for a registration
// step (fire events in index order, then cancel) when WaitAny subscribes.
type harness struct {
	events   []*testEvent
	fireStep []int // per event: step at which it fires, or never
	cancelAt int   // step at which ctx is cancelled, never, or waitStep
	cancel   context.CancelFunc
}

func (h *harness) runStep(step int) {
	for idx, fs := range h.fireStep {
		if fs == step {
			h.events[idx].Fire()
		}
	}
	if h.cancelAt == step {
		h.cancel()
	}
}

// hookEvent wraps the testEvent for index step and drives the schedule.
type hookEvent struct {
	h    *harness
	step int
}

func (he hookEvent) Subscribe(cb func()) waitany.Subscription {
	he.h.runStep(he.step)
	sub := he.h.events[he.step].Subscribe(cb)
	if he.step == len(he.h.events)-1 {
		// Post-registration hook: step N runs after the last Subscribe.
		he.h.runStep(len(he.h.events))
	}
	return sub
}

// simulate computes the expected outcome of a schedule by mirroring the
// control flow of WaitAny exactly.
func simulate(n int, fireStep []int, cancelAt int) (winner int, cancelled bool) {
	winner = -1
	fired := make([]bool, n)
	deliver := func(idx int) {
		if winner == -1 {
			winner = idx
		}
	}
	cancelled = false
	for i := 0; i < n; i++ {
		if winner != -1 {
			break
		}
		if cancelled {
			return -1, true
		}
		// Hook i: fire scheduled events; those subscribed earlier
		// (idx < i) deliver immediately, in index order.
		for idx := 0; idx < n; idx++ {
			if fireStep[idx] == i {
				fired[idx] = true
				if idx < i {
					deliver(idx)
				}
			}
		}
		if cancelAt == i {
			cancelled = true
		}
		// Subscribing event i delivers synchronously if it already fired.
		if fired[i] {
			deliver(i)
		}
		if i == n-1 {
			// Post-registration hook (step N): every event is subscribed.
			for idx := 0; idx < n; idx++ {
				if fireStep[idx] == n {
					fired[idx] = true
					deliver(idx)
				}
			}
			if cancelAt == n {
				cancelled = true
			}
		}
	}
	if winner != -1 {
		return winner, false
	}
	if cancelled || cancelAt == waitStep {
		return -1, true
	}
	return -1, false // would block forever; not a runnable schedule
}

func scheduleName(fireStep []int, cancelAt int) string {
	parts := make([]string, 0, len(fireStep)+1)
	for i, fs := range fireStep {
		parts = append(parts, fmt.Sprintf("ev%d@%s", i, stepName(fs)))
	}
	parts = append(parts, "cancel@"+stepName(cancelAt))
	return strings.Join(parts, ",")
}

func stepName(step int) string {
	switch step {
	case never:
		return "never"
	case waitStep:
		return "wait"
	default:
		return fmt.Sprint(step)
	}
}

func TestScheduleEnumeration(t *testing.T) {
	const n = 3
	fireOptions := []int{never, 0, 1, 2, 3} // fire at hook 0..N or never
	cancelOptions := []int{never, waitStep, 0, 1, 2, 3}

	run := 0
	for _, f0 := range fireOptions {
		for _, f1 := range fireOptions {
			for _, f2 := range fireOptions {
				for _, cancelAt := range cancelOptions {
					fireStep := []int{f0, f1, f2}
					wantWinner, wantCancelled := simulate(n, fireStep, cancelAt)
					if wantWinner == -1 && !wantCancelled {
						continue // WaitAny would block forever; skip
					}
					run++
					t.Run(scheduleName(fireStep, cancelAt), func(t *testing.T) {
						h := &harness{fireStep: fireStep, cancelAt: cancelAt}
						for i := 0; i < n; i++ {
							h.events = append(h.events, &testEvent{})
						}
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						h.cancel = cancel

						events := make([]waitany.Event, n)
						for i := range events {
							events[i] = hookEvent{h: h, step: i}
						}

						if cancelAt == waitStep {
							// Cancel only after WaitAny has reached the
							// waiting phase; the outcome is independent of
							// the exact moment.
							go func() {
								time.Sleep(5 * time.Millisecond)
								cancel()
							}()
						}

						got, err := waitany.WaitAny(ctx, events...)

						if wantCancelled {
							if err != context.Canceled || got != -1 {
								t.Fatalf("got (%d, %v), want cancellation", got, err)
							}
						} else {
							if err != nil || got != wantWinner {
								t.Fatalf("got (%d, %v), want (%d, nil)", got, err, wantWinner)
							}
						}

						// Invariants: no residual subscriptions, every
						// issued subscription released exactly once, and no
						// callback delivered more than once per Subscribe.
						for i, ev := range h.events {
							if ev.activeSubs != 0 {
								t.Errorf("event %d: %d residual subscriptions", i, ev.activeSubs)
							}
							if ev.unsubs != ev.subsIssued {
								t.Errorf("event %d: %d unsubs for %d issued subs", i, ev.unsubs, ev.subsIssued)
							}
							if ev.cbDelivered > ev.subscribes {
								t.Errorf("event %d: %d callback deliveries for %d subscribes", i, ev.cbDelivered, ev.subscribes)
							}
						}
					})
				}
			}
		}
	}
	t.Logf("executed %d schedules", run)
}

// ---------------------------------------------------------------------------
// Targeted tests
// ---------------------------------------------------------------------------

// misbehavingEvent invokes its callback more than once.
type misbehavingEvent struct {
	ready chan struct{} // closed once Subscribe has run
	sub   *countingSub
	cb    func()
}

type countingSub struct{ unsubs int }

func (s *countingSub) Unsubscribe() { s.unsubs++ }

func (m *misbehavingEvent) Subscribe(cb func()) waitany.Subscription {
	m.cb = cb
	m.sub = &countingSub{}
	close(m.ready)
	return m.sub
}

func (m *misbehavingEvent) fire(times int) {
	for i := 0; i < times; i++ {
		m.cb()
	}
}

func TestDuplicateCallback(t *testing.T) {
	bad := &misbehavingEvent{ready: make(chan struct{})}
	good := &testEvent{}

	done := make(chan int, 1)
	go func() {
		got, err := waitany.WaitAny(context.Background(), bad, good)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		done <- got
	}()

	// Wait until WaitAny has registered, then fire repeatedly.
	<-bad.ready
	bad.fire(3)

	select {
	case got := <-done:
		if got != 0 {
			t.Fatalf("got winner %d, want 0", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitAny did not return")
	}
	select {
	case <-done:
		t.Fatal("result delivered more than once")
	default:
	}
	if bad.sub.unsubs != 1 {
		t.Fatalf("got %d unsubs, want exactly 1", bad.sub.unsubs)
	}
	if good.activeSubs != 0 {
		t.Fatalf("good event has %d residual subscriptions", good.activeSubs)
	}
}

func TestNoEvents(t *testing.T) {
	got, err := waitany.WaitAny(context.Background())
	if err != waitany.ErrNoEvents || got != -1 {
		t.Fatalf("got (%d, %v), want (-1, ErrNoEvents)", got, err)
	}
}

func TestPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ev := &testEvent{}
	got, err := waitany.WaitAny(ctx, ev)
	if err != context.Canceled || got != -1 {
		t.Fatalf("got (%d, %v), want (-1, context.Canceled)", got, err)
	}
	if ev.subscribes != 0 {
		t.Fatalf("event subscribed %d times despite pre-cancelled ctx", ev.subscribes)
	}
}

// asyncEvent is a thread-safe one-shot event source for the concurrency
// smoke test.
type asyncEvent struct {
	mu         sync.Mutex
	fired      bool
	cbs        []func()
	activeSubs int
}

func (e *asyncEvent) Subscribe(cb func()) waitany.Subscription {
	e.mu.Lock()
	if e.fired {
		e.mu.Unlock()
		cb()
		return nil
	}
	e.cbs = append(e.cbs, cb)
	e.activeSubs++
	e.mu.Unlock()
	return &asyncSub{ev: e, cb: cb}
}

func (e *asyncEvent) fire() {
	e.mu.Lock()
	if e.fired {
		e.mu.Unlock()
		return
	}
	e.fired = true
	cbs := e.cbs
	e.cbs = nil
	e.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

func (e *asyncEvent) residual() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.activeSubs
}

type asyncSub struct {
	ev   *asyncEvent
	cb   func()
	once sync.Once
}

func (s *asyncSub) Unsubscribe() {
	s.once.Do(func() {
		s.ev.mu.Lock()
		defer s.ev.mu.Unlock()
		for i, cb := range s.ev.cbs {
			if fmt.Sprintf("%p", cb) == fmt.Sprintf("%p", s.cb) {
				s.ev.cbs = append(s.ev.cbs[:i], s.ev.cbs[i+1:]...)
				break
			}
		}
		s.ev.activeSubs--
	})
}

func TestConcurrentSmoke(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		const n = 5
		events := make([]*asyncEvent, n)
		for i := range events {
			events[i] = &asyncEvent{}
		}
		ctx, cancel := context.WithCancel(context.Background())

		type result struct {
			idx int
			err error
		}
		resCh := make(chan result, 1)
		go func() {
			evs := make([]waitany.Event, n)
			for i, ev := range events {
				evs[i] = ev
			}
			idx, err := waitany.WaitAny(ctx, evs...)
			resCh <- result{idx, err}
		}()

		// Fire a random subset of events and cancel concurrently.
		var wg sync.WaitGroup
		for i, ev := range events {
			if (iter>>i)&1 == 0 {
				continue
			}
			wg.Add(1)
			go func(ev *asyncEvent) {
				defer wg.Done()
				ev.fire()
			}(ev)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel()
		}()
		wg.Wait()

		var res result
		select {
		case res = <-resCh:
		case <-time.After(2 * time.Second):
			t.Fatal("WaitAny did not return")
		}
		if res.err != nil && res.err != context.Canceled {
			t.Fatalf("unexpected error: %v", res.err)
		}
		if res.err == nil && (res.idx < 0 || res.idx >= n) {
			t.Fatalf("invalid winner index %d", res.idx)
		}
		for i, ev := range events {
			if r := ev.residual(); r != 0 {
				t.Fatalf("event %d: %d residual subscriptions", i, r)
			}
		}
		cancel()
	}
}
