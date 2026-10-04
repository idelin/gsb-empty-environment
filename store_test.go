package statewait

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func waitTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestWaitImmediateWhenSatisfied(t *testing.T) {
	s := New(10)
	res, err := s.Wait(waitTimeout(t, time.Second), func(v int) bool { return v >= 5 })
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.State != 10 || res.Version != 0 {
		t.Fatalf("got %+v, want state=10 version=0", res)
	}
	if n := s.PendingWaiters(); n != 0 {
		t.Fatalf("PendingWaiters = %d, want 0", n)
	}
}

func TestWaitBlocksUntilUpdate(t *testing.T) {
	s := New(0)
	done := make(chan Result[int], 1)
	go func() {
		res, err := s.Wait(waitTimeout(t, 5*time.Second), func(v int) bool { return v >= 100 })
		if err != nil {
			t.Errorf("Wait: %v", err)
			return
		}
		done <- res
	}()

	deadline := time.Now().Add(2 * time.Second)
	for s.PendingWaiters() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.PendingWaiters() != 1 {
		t.Fatal("waiter was not registered")
	}

	s.Update(func(v int) int { return v + 100 })

	select {
	case res := <-done:
		if res.State != 100 || res.Version != 1 {
			t.Fatalf("got %+v, want state=100 version=1", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter was not woken by satisfying update")
	}
	if n := s.PendingWaiters(); n != 0 {
		t.Fatalf("PendingWaiters = %d, want 0", n)
	}
}

// TestNoLostWakeup hammers the check-then-register window: waiters and
// updaters start concurrently, and every waiter must eventually observe a
// satisfying state instead of sleeping forever.
func TestNoLostWakeup(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		s := New(0)
		const waiters = 8
		var wg sync.WaitGroup
		errs := make(chan error, waiters)
		for i := 0; i < waiters; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := s.Wait(waitTimeout(t, 5*time.Second), func(v int) bool { return v == 1 })
				if err != nil {
					errs <- err
					return
				}
				if res.State != 1 {
					errs <- fmt.Errorf("observed state %d, want 1", res.State)
				}
			}()
		}
		// Race the update against registration: sometimes the update
		// lands before any waiter registers, sometimes after.
		if iter%2 == 0 {
			s.Update(func(v int) int { return 1 })
		} else {
			go s.Update(func(v int) int { return 1 })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if n := s.PendingWaiters(); n != 0 {
			t.Fatalf("iter %d: %d waiters left behind", iter, n)
		}
	}
}

func TestWaitCancel(t *testing.T) {
	s := New(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.Wait(ctx, func(v int) bool { return v > 0 })
		done <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for s.PendingWaiters() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled wait did not return")
	}
	if n := s.PendingWaiters(); n != 0 {
		t.Fatalf("PendingWaiters = %d, want 0 after cancel", n)
	}
}

// TestCancelVsSatisfyRace forces cancellation and satisfaction to collide.
// Each wait must return exactly once, with either a satisfying state or a
// context error, and no waiter may leak.
func TestCancelVsSatisfyRace(t *testing.T) {
	for iter := 0; iter < 500; iter++ {
		s := New(0)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			res, err := s.Wait(ctx, func(v int) bool { return v == 1 })
			if err == nil && res.State != 1 {
				done <- fmt.Errorf("success with non-satisfying state %d", res.State)
				return
			}
			done <- err
		}()
		go cancel()
		go s.Update(func(v int) int { return 1 })

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: wait never returned", iter)
		}
		cancel()
		if n := s.PendingWaiters(); n != 0 {
			t.Fatalf("iter %d: %d waiters leaked", iter, n)
		}
	}
}

func TestUpdateWakesOnlyMatching(t *testing.T) {
	s := New(0)
	got := make(chan int, 2)
	for _, threshold := range []int{10, 100} {
		threshold := threshold
		go func() {
			res, err := s.Wait(waitTimeout(t, 5*time.Second), func(v int) bool { return v >= threshold })
			if err != nil {
				t.Errorf("Wait(>=%d): %v", threshold, err)
				return
			}
			got <- res.State
		}()
	}

	deadline := time.Now().Add(2 * time.Second)
	for s.PendingWaiters() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	s.Update(func(v int) int { return 50 })
	select {
	case v := <-got:
		if v != 50 {
			t.Fatalf("woke with state %d, want 50", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("threshold-10 waiter not woken")
	}
	if n := s.PendingWaiters(); n != 1 {
		t.Fatalf("PendingWaiters = %d, want 1 (threshold-100 still waiting)", n)
	}

	s.Update(func(v int) int { return v + 50 })
	select {
	case v := <-got:
		if v != 100 {
			t.Fatalf("woke with state %d, want 100", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("threshold-100 waiter not woken")
	}
}

// TestHistoryConsistency runs concurrent updaters and waiters, then checks
// every successful wait against the event history: the reported version
// must name a real committed event whose state is exactly the observed
// state, the predicate must hold for it, and the version must be at least
// as new as the version visible when the wait started (no stale reads).
func TestHistoryConsistency(t *testing.T) {
	s := New(0)
	const updaters = 4
	const updatesPerUpdater = 250
	const waiters = 64

	var wg sync.WaitGroup
	for i := 0; i < updaters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < updatesPerUpdater; j++ {
				s.Update(func(v int) int { return v + 1 })
			}
		}()
	}

	type observation struct {
		before uint64
		res    Result[int]
	}
	obs := make([]observation, waiters)
	var wwg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wwg.Add(1)
		go func(i int) {
			defer wwg.Done()
			threshold := 1 + (i * (updaters * updatesPerUpdater) / waiters)
			before := s.Get().Version
			res, err := s.Wait(waitTimeout(t, 10*time.Second), func(v int) bool { return v >= threshold })
			if err != nil {
				t.Errorf("waiter %d (threshold %d): %v", i, threshold, err)
				return
			}
			obs[i] = observation{before: before, res: res}
		}(i)
	}
	wg.Wait()
	wwg.Wait()

	history := s.History()
	if want := uint64(updaters * updatesPerUpdater); history[len(history)-1].Version != want {
		t.Fatalf("final version = %d, want %d", history[len(history)-1].Version, want)
	}
	for i, e := range history {
		if e.Version != uint64(i) || e.State != i {
			t.Fatalf("history[%d] = %+v, want version=%d state=%d", i, e, i, i)
		}
	}

	for i, o := range obs {
		if o.res.Version == 0 && o.before == 0 {
			// Waiter registered before any update but was never
			// satisfied: impossible, all thresholds are reachable.
			t.Errorf("waiter %d: no observation recorded", i)
			continue
		}
		if int(o.res.Version) >= len(history) {
			t.Errorf("waiter %d: version %d not in history (len %d)", i, o.res.Version, len(history))
			continue
		}
		e := history[o.res.Version]
		if e.State != o.res.State {
			t.Errorf("waiter %d: observed state %d but history at version %d has %d",
				i, o.res.State, o.res.Version, e.State)
		}
		threshold := 1 + (i * (updaters * updatesPerUpdater) / waiters)
		if o.res.State < threshold {
			t.Errorf("waiter %d: predicate false for observed state %d (threshold %d)",
				i, o.res.State, threshold)
		}
		if o.res.Version < o.before {
			t.Errorf("waiter %d: stale result version %d, wait started at version %d",
				i, o.res.Version, o.before)
		}
	}
	if n := s.PendingWaiters(); n != 0 {
		t.Fatalf("PendingWaiters = %d, want 0", n)
	}
}
