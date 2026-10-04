package waitfirst_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waitfirst"
)

// makeEvents returns n un-fired one-shot events.
func makeEvents(n int) []*waitfirst.OneShot {
	events := make([]*waitfirst.OneShot, n)
	for i := range events {
		events[i] = waitfirst.NewOneShot()
	}
	return events
}

func asEvents(shots []*waitfirst.OneShot) []waitfirst.Event {
	events := make([]waitfirst.Event, len(shots))
	for i, s := range shots {
		events[i] = s
	}
	return events
}

func assertNoPending(t *testing.T, shots []*waitfirst.OneShot) {
	t.Helper()
	for i, s := range shots {
		if p := s.Pending(); p != 0 {
			t.Errorf("event %d: %d subscriptions left behind", i, p)
		}
	}
}

func TestWaitFirstSingleCompletion(t *testing.T) {
	shots := makeEvents(3)
	go func() {
		time.Sleep(10 * time.Millisecond)
		shots[2].Fire()
	}()
	idx, err := waitfirst.WaitFirst(context.Background(), asEvents(shots))
	if err != nil || idx != 2 {
		t.Fatalf("got (%d, %v), want (2, nil)", idx, err)
	}
	assertNoPending(t, shots)
}

func TestWaitFirstPrefiredEvents(t *testing.T) {
	// Events 0 and 2 complete before the wait starts: the lowest index
	// must win.
	shots := makeEvents(3)
	shots[0].Fire()
	shots[2].Fire()
	idx, err := waitfirst.WaitFirst(context.Background(), asEvents(shots))
	if err != nil || idx != 0 {
		t.Fatalf("got (%d, %v), want (0, nil)", idx, err)
	}
	assertNoPending(t, shots)
}

func TestWaitFirstTieBreakByInputOrder(t *testing.T) {
	// Events 1 and 2 complete before the wait starts; event 0 never
	// fires. The tie is resolved by input order.
	shots := makeEvents(3)
	shots[1].Fire()
	shots[2].Fire()
	idx, err := waitfirst.WaitFirst(context.Background(), asEvents(shots))
	if err != nil || idx != 1 {
		t.Fatalf("got (%d, %v), want (1, nil)", idx, err)
	}
	assertNoPending(t, shots)
}

// fireDuringSubscribe wraps shots[i]: when subscribed, it first fires
// trigger, then subscribes to shots[i]. It models an event completing
// while WaitFirst is still registering the remaining events.
func fireDuringSubscribe(trigger *waitfirst.OneShot, inner waitfirst.Event) waitfirst.Event {
	return subscribeFunc(func(fn func()) func() {
		unsub := inner.Subscribe(fn)
		trigger.Fire()
		return unsub
	})
}

type subscribeFunc func(func()) func()

func (f subscribeFunc) Subscribe(fn func()) func() { return f(fn) }

func TestWaitFirstCompletionDuringRegistration(t *testing.T) {
	// Event 2 is pre-fired; event 0 fires while event 1 is being
	// registered. Both 0 and 2 are complete at decision time, so the
	// lower index 0 must win even though 2 completed first.
	shots := makeEvents(3)
	shots[2].Fire()
	events := asEvents(shots)
	events[1] = fireDuringSubscribe(shots[0], shots[1])
	idx, err := waitfirst.WaitFirst(context.Background(), events)
	if err != nil || idx != 0 {
		t.Fatalf("got (%d, %v), want (0, nil)", idx, err)
	}
	assertNoPending(t, shots)
}

func TestWaitFirstCancellation(t *testing.T) {
	shots := makeEvents(3)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	idx, err := waitfirst.WaitFirst(ctx, asEvents(shots))
	if idx != -1 || err != context.Canceled {
		t.Fatalf("got (%d, %v), want (-1, context.Canceled)", idx, err)
	}
	assertNoPending(t, shots)
}

func TestWaitFirstNoEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	idx, err := waitfirst.WaitFirst(ctx, nil)
	if idx != -1 || err != context.DeadlineExceeded {
		t.Fatalf("got (%d, %v), want (-1, context.DeadlineExceeded)", idx, err)
	}
}

func TestWaitFirstCompletionBeatsCancellation(t *testing.T) {
	// A completed event must win even if the context is already
	// cancelled when the wait observes it.
	shots := makeEvents(2)
	shots[1].Fire()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	idx, err := waitfirst.WaitFirst(ctx, asEvents(shots))
	if err != nil || idx != 1 {
		t.Fatalf("got (%d, %v), want (1, nil)", idx, err)
	}
	assertNoPending(t, shots)
}

func TestWaitFirstUnsubscribesLosers(t *testing.T) {
	// After the wait returns, firing the remaining events must not
	// invoke their callbacks.
	shots := makeEvents(3)
	var calls [3]atomic.Int32
	events := make([]waitfirst.Event, 3)
	for i, s := range shots {
		i, s := i, s
		events[i] = subscribeFunc(func(fn func()) func() {
			return s.Subscribe(func() {
				calls[i].Add(1)
				fn()
			})
		})
	}
	shots[0].Fire()
	idx, err := waitfirst.WaitFirst(context.Background(), events)
	if err != nil || idx != 0 {
		t.Fatalf("got (%d, %v), want (0, nil)", idx, err)
	}
	shots[1].Fire()
	shots[2].Fire()
	if got := calls[1].Load(); got != 0 {
		t.Errorf("event 1: callback ran %d times after unsubscribe", got)
	}
	if got := calls[2].Load(); got != 0 {
		t.Errorf("event 2: callback ran %d times after unsubscribe", got)
	}
	if got := calls[0].Load(); got != 1 {
		t.Errorf("event 0: callback ran %d times, want 1", got)
	}
	assertNoPending(t, shots)
}

// TestWaitFirstStress hammers WaitFirst with concurrent fires and
// cancellations, checking the invariants under -race.
func TestWaitFirstStress(t *testing.T) {
	for iter := 0; iter < 2000; iter++ {
		const n = 4
		shots := makeEvents(n)
		var calls [n]atomic.Int32
		events := make([]waitfirst.Event, n)
		for i, s := range shots {
			i, s := i, s
			events[i] = subscribeFunc(func(fn func()) func() {
				return s.Subscribe(func() {
					calls[i].Add(1)
					fn()
				})
			})
		}
		ctx, cancel := context.WithCancel(context.Background())

		var wg sync.WaitGroup
		for i, s := range shots {
			i, s := i, s
			wg.Add(1)
			go func() {
				defer wg.Done()
				if (iter>>i)%3 != 0 { // leave some events unfired
					s.Fire()
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if iter%2 == 0 {
				cancel()
			}
		}()

		idx, err := waitfirst.WaitFirst(ctx, events)
		wg.Wait()
		cancel()

		for i := range calls {
			if c := calls[i].Load(); c > 1 {
				t.Fatalf("iter %d: event %d callback ran %d times", iter, i, c)
			}
		}
		assertNoPending(t, shots)
		if err == nil {
			if idx < 0 || idx >= n || !shots[idx].Fired() {
				t.Fatalf("iter %d: invalid result %d", iter, idx)
			}
		} else if err != context.Canceled {
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}
	}
}

func ExampleWaitFirst() {
	events := []*waitfirst.OneShot{
		waitfirst.NewOneShot(),
		waitfirst.NewOneShot(),
	}
	go func() {
		time.Sleep(time.Millisecond)
		events[1].Fire()
	}()
	idx, _ := waitfirst.WaitFirst(context.Background(), asEvents(events))
	fmt.Println(idx)
	// Output: 1
}
