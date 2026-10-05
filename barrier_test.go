package barrier

import (
	"sync"
	"testing"
	"time"
)

// recvResult fails the test if no result arrives within the timeout.
func recvResult(t *testing.T, ch <-chan Result) Result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for barrier result")
		return Result{}
	}
}

func TestBasicRelease(t *testing.T) {
	b := New(3)
	results := make(chan Result, 3)
	for i := 0; i < 3; i++ {
		go func() { results <- b.Arrive() }()
	}
	for i := 0; i < 3; i++ {
		r := recvResult(t, results)
		if r.Round != 0 || r.Reason != Released {
			t.Fatalf("got (%d, %v), want (0, released)", r.Round, r.Reason)
		}
	}
	if b.Round() != 1 {
		t.Fatalf("round = %d, want 1", b.Round())
	}

	// The barrier is reusable: round 1 works the same way.
	for i := 0; i < 3; i++ {
		go func() { results <- b.Arrive() }()
	}
	for i := 0; i < 3; i++ {
		r := recvResult(t, results)
		if r.Round != 1 || r.Reason != Released {
			t.Fatalf("got (%d, %v), want (1, released)", r.Round, r.Reason)
		}
	}
}

func TestCancelWakesAllWaiters(t *testing.T) {
	b := New(3)
	results := make(chan Result, 2)
	go func() { results <- b.Arrive() }()
	go func() { results <- b.Arrive() }()

	// Give both goroutines a chance to block on the barrier.
	time.Sleep(50 * time.Millisecond)
	if round := b.Cancel(); round != 0 {
		t.Fatalf("Cancel returned %d, want 0", round)
	}

	for i := 0; i < 2; i++ {
		r := recvResult(t, results)
		if r.Round != 0 || r.Reason != Cancelled {
			t.Fatalf("got (%d, %v), want (0, cancelled)", r.Round, r.Reason)
		}
	}

	// The barrier advanced to round 1 and is still usable.
	if b.Round() != 1 || b.Parties() != 3 {
		t.Fatalf("round=%d parties=%d, want 1/3", b.Round(), b.Parties())
	}
	r := make(chan Result, 3)
	for i := 0; i < 3; i++ {
		go func() { r <- b.Arrive() }()
	}
	for i := 0; i < 3; i++ {
		if res := recvResult(t, r); res.Round != 1 || res.Reason != Released {
			t.Fatalf("got (%d, %v), want (1, released)", res.Round, res.Reason)
		}
	}
}

func TestArriveAndLeave(t *testing.T) {
	b := New(2)
	results := make(chan Result, 2)
	go func() { results <- b.ArriveAndLeave() }() // leaver
	go func() { results <- b.Arrive() }()         // stayer

	for i := 0; i < 2; i++ {
		r := recvResult(t, results)
		if r.Round != 0 || r.Reason != Released {
			t.Fatalf("got (%d, %v), want (0, released)", r.Round, r.Reason)
		}
	}

	// The departure applies from round 1: only one party remains, so a
	// single arrival releases the round immediately.
	if got := b.Parties(); got != 1 {
		t.Fatalf("parties = %d, want 1", got)
	}
	r := recvResult(t, waitArrive(b))
	if r.Round != 1 || r.Reason != Released {
		t.Fatalf("got (%d, %v), want (1, released)", r.Round, r.Reason)
	}
}

func waitArrive(b *Barrier) <-chan Result {
	ch := make(chan Result, 1)
	go func() { ch <- b.Arrive() }()
	return ch
}

func TestJoinTakesEffectNextRound(t *testing.T) {
	b := New(2)

	joined := make(chan int64, 1)
	go func() { joined <- b.Join() }()

	// The joiner must not count toward the current round: two arrivals
	// are still required to release round 0.
	results := make(chan Result, 2)
	go func() { results <- b.Arrive() }()
	time.Sleep(50 * time.Millisecond)
	select {
	case r := <-joined:
		t.Fatalf("Join returned for round %d before round 0 settled", r)
	default:
	}
	go func() { results <- b.Arrive() }()

	for i := 0; i < 2; i++ {
		if r := recvResult(t, results); r.Round != 0 || r.Reason != Released {
			t.Fatalf("got (%d, %v), want (0, released)", r.Round, r.Reason)
		}
	}
	select {
	case round := <-joined:
		if round != 1 {
			t.Fatalf("Join returned round %d, want 1", round)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Join did not return after round 0 settled")
	}

	// Round 1 expects three participants now.
	if got := b.Parties(); got != 3 {
		t.Fatalf("parties = %d, want 3", got)
	}
	r := make(chan Result, 3)
	for i := 0; i < 3; i++ {
		go func() { r <- b.Arrive() }()
	}
	for i := 0; i < 3; i++ {
		if res := recvResult(t, r); res.Round != 1 || res.Reason != Released {
			t.Fatalf("got (%d, %v), want (1, released)", res.Round, res.Reason)
		}
	}
}

// TestLastArrivalAndCancelRaceSameRound forces the last arrival and a
// cancellation to race on the same round while a departure is pending.
// Exactly one of them may settle the round; the pending departure must be
// applied exactly once either way, so the barrier ends in round 2 expecting
// exactly one party.
func TestLastArrivalAndCancelRaceSameRound(t *testing.T) {
	for iter := 0; iter < 2000; iter++ {
		b := New(2)

		leaver := make(chan Result, 1)
		go func() { leaver <- b.ArriveAndLeave() }()
		waitForArrived(t, b, 1)

		var wg sync.WaitGroup
		arriver := make(chan Result, 1)
		cancelled := make(chan int64, 1)
		wg.Add(2)
		go func() { defer wg.Done(); arriver <- b.Arrive() }()
		go func() { defer wg.Done(); cancelled <- b.Cancel() }()
		wg.Wait()

		rl, ra := <-leaver, <-arriver
		cancelRound := <-cancelled

		// Round 0 settled exactly once: whoever waited on it agrees on
		// the reason, and the loser of the race settled round 1 instead.
		if rl.Round != 0 {
			t.Fatalf("iter %d: leaver settled round %d, want 0", iter, rl.Round)
		}
		switch {
		case ra.Round == 0 && cancelRound == 1:
			if ra.Reason != rl.Reason {
				t.Fatalf("iter %d: round 0 settled as %v and %v", iter, ra.Reason, rl.Reason)
			}
		case ra.Round == 1 && cancelRound == 0:
			if rl.Reason != Cancelled || ra.Reason != Released {
				t.Fatalf("iter %d: got leaver=%v arriver=%v", iter, rl.Reason, ra.Reason)
			}
		default:
			t.Fatalf("iter %d: impossible outcome arriver round %d, cancelled round %d",
				iter, ra.Round, cancelRound)
		}

		// The departure was applied exactly once: 2 - 1 = 1, never 0 or 2.
		if got := b.Parties(); got != 1 {
			t.Fatalf("iter %d: parties = %d, want 1 (double decrement?)", iter, got)
		}
		if got := b.Round(); got != 2 {
			t.Fatalf("iter %d: round = %d, want 2", iter, got)
		}
	}
}

// TestConcurrentArrivalLeaveCancel fires an arrival-with-departure, a plain
// arrival and a cancellation at the barrier with no ordering guarantees.
// A watchdog keeps cancelling so no split round can block forever. Whatever
// the interleaving, the single pending departure must be applied exactly
// once: the barrier must end up expecting exactly one party.
func TestConcurrentArrivalLeaveCancel(t *testing.T) {
	for iter := 0; iter < 2000; iter++ {
		b := New(2)
		var wg sync.WaitGroup
		results := make(chan Result, 2)

		done := make(chan struct{})
		wg.Add(3)
		go func() { defer wg.Done(); results <- b.ArriveAndLeave() }()
		go func() { defer wg.Done(); results <- b.Arrive() }()
		go func() { defer wg.Done(); b.Cancel() }()
		go func() { wg.Wait(); close(done) }()
		go func() { // watchdog: unstick any round split by the first cancel
			for {
				select {
				case <-done:
					return
				case <-time.After(time.Millisecond):
					b.Cancel()
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iter %d: deadlock", iter)
		}

		r1, r2 := <-results, <-results
		if r1.Round == r2.Round && r1.Reason != r2.Reason {
			t.Fatalf("iter %d: round %d settled twice: %v and %v",
				iter, r1.Round, r1.Reason, r2.Reason)
		}
		if got := b.Parties(); got != 1 {
			t.Fatalf("iter %d: parties = %d, want 1 (double decrement?)", iter, got)
		}
	}
}

func waitForArrived(t *testing.T, b *Barrier, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for b.Arrived() != n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d arrivals", n)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStress mixes arrivals, departures, joins and cancels across many
// rounds and asserts the barrier never deadlocks and rounds stay monotonic.
func TestStress(t *testing.T) {
	b := New(4)
	var wg sync.WaitGroup
	stop := time.After(2 * time.Second)

	// Cancel periodically; every cancelled round must wake its waiters.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				b.Cancel()
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Workers loop: arrive, sometimes declare departure, then rejoin.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var last int64 = -1
			for round := 0; round < 50; round++ {
				var r Result
				if round%7 == 3 {
					r = b.ArriveAndLeave()
				} else {
					r = b.Arrive()
				}
				if r.Round <= last {
					t.Errorf("non-monotonic round: %d after %d", r.Round, last)
					return
				}
				last = r.Round
				if round%7 == 3 {
					b.Join() // rejoin from the next round
				}
			}
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("stress test deadlocked")
	}
}
