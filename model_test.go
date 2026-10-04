package waitfirst_test

// This file verifies WaitFirst by exhaustively enumerating thread
// interleavings of a model that mirrors the real implementation step by
// step (see waitfirst.go). Each Step in the model corresponds to one
// atomic step in the real code:
//
//	model                          real code
//	-----                          ---------
//	subscribe step per event       events[i].Subscribe(...) in WaitFirst
//	scan step                      the scan loop over completed[i].Load()
//	BlockUntil(any|canceled)       select on wake / ctx.Done()
//	unsubscribe step per event     the deferred unsubscribe calls
//	event thread fire step         OneShot.Fire delivering the callback
//	cancel thread step             ctx cancellation
//
// Because the scheduler serializes threads, plain variables model the
// atomics and the event-source lock. After every run, the check function
// asserts the invariants: no duplicate callbacks, no leftover
// subscriptions, and no missed completed events.

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"waitfirst/internal/sched"
)

func TestWaitFirstAllInterleavings(t *testing.T) {
	// The scheduler is cooperative: a single OS thread makes the
	// goroutine ping-pong an order of magnitude cheaper.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	cases := []struct {
		events         int
		maxPreemptions int // negative = exhaustive
	}{
		// Small configurations are explored exhaustively; larger ones
		// use preemption bounding (as in CHESS) to stay tractable while
		// still covering the interleavings where races can occur.
		{1, -1},
		{2, -1},
		{3, 3},
		{4, 2},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("events=%d", tc.events), func(t *testing.T) {
			runs, err := sched.Explore(func(s *sched.Sched) func() {
				return waitFirstModel(t, s, tc.events)
			}, sched.Config{MaxPreemptions: tc.maxPreemptions})
			if err != nil {
				t.Fatalf("explore: %v", err)
			}
			t.Logf("explored %d schedules", runs)
		})
	}
}

// waitFirstModel runs one modeled execution of WaitFirst against n
// one-shot events plus an external cancellation, and returns the check
// function for the run's final state.
func waitFirstModel(t *testing.T, s *sched.Sched, n int) func() {
	fired := make([]bool, n)      // event source has completed
	subscribed := make([]bool, n) // registration still active at the source
	completed := make([]bool, n)  // WaitFirst has observed the completion
	callbacks := make([]int, n)   // how often each callback ran
	canceled := false

	anyCompleted := func() bool {
		for _, c := range completed {
			if c {
				return true
			}
		}
		return false
	}

	// One thread per event source: fires the event once. Delivering the
	// callback is atomic with firing, matching OneShot.Fire, which
	// collects and invokes callbacks while holding its lock semantics.
	for i := 0; i < n; i++ {
		i := i
		s.Go(func() {
			s.Step()
			fired[i] = true
			if subscribed[i] {
				subscribed[i] = false // registration consumed by delivery
				callbacks[i]++
				completed[i] = true
			}
		})
	}

	// External cancellation thread.
	s.Go(func() {
		s.Step()
		canceled = true
	})

	// The decision state, snapshotted at the moment WaitFirst returns.
	result := -2 // -2 = has not returned yet
	resultErr := error(nil)
	decision := make([]bool, n) // completed[] as observed at decision time

	// Waiter thread: mirrors WaitFirst.
	s.Go(func() {
		// Registration phase: one step per Subscribe call. Subscribing
		// to an already-fired event runs the callback synchronously.
		for i := 0; i < n; i++ {
			s.Step()
			if fired[i] {
				callbacks[i]++
				completed[i] = true
			} else {
				subscribed[i] = true
			}
		}

		scan := func() int {
			for i := 0; i < n; i++ {
				if completed[i] {
					return i
				}
			}
			return -1
		}

		for {
			s.Step() // scan
			if idx := scan(); idx >= 0 {
				result, resultErr = idx, nil
				break
			}
			// select on wake / ctx.Done().
			s.BlockUntil(func() bool { return anyCompleted() || canceled })
			s.Step() // re-scan after waking (incl. final scan on cancel)
			if idx := scan(); idx >= 0 {
				result, resultErr = idx, nil
			} else {
				// Only cancellation can have woken us.
				result, resultErr = -1, context.Canceled
			}
			break
		}
		copy(decision, completed)

		// Teardown: revoke every registration, one step per call.
		for i := 0; i < n; i++ {
			s.Step()
			subscribed[i] = false
		}
	})

	return func() {
		if result == -2 {
			t.Error("WaitFirst did not return")
		}
		for i := 0; i < n; i++ {
			if callbacks[i] > 1 {
				t.Errorf("event %d: callback ran %d times", i, callbacks[i])
			}
			if subscribed[i] {
				t.Errorf("event %d: subscription left behind after return", i)
			}
		}
		if resultErr == nil {
			if result < 0 || result >= n {
				t.Errorf("invalid result index %d", result)
				return
			}
			if !decision[result] {
				t.Errorf("returned %d but event %d had not completed at decision time", result, result)
			}
			for j := 0; j < result; j++ {
				if decision[j] {
					t.Errorf("returned %d but earlier event %d had completed at decision time", result, j)
				}
			}
		} else {
			if !canceled {
				t.Error("returned cancellation error without cancellation")
			}
			for j := 0; j < n; j++ {
				if decision[j] {
					t.Errorf("missed completed event %d: returned cancellation while it was observable", j)
				}
			}
		}
	}
}
