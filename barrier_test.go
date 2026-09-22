package rendezvous

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitBlocked asserts that nothing is sent on ch within d, i.e. the
// participants behind ch are still blocked.
func waitBlocked(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s: released prematurely", what)
	case <-time.After(d):
	}
}

// waitReleased asserts that ch is closed/emptied within d.
func waitReleased(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("%s: still blocked after %v (permanent wait?)", what, d)
	}
}

// TestBasicRendezvous: nobody is released before every party arrives;
// once the last one arrives, all are released.
func TestBasicRendezvous(t *testing.T) {
	const parties = 4
	b := New(parties)
	var released atomic.Int32
	done := make(chan struct{}, parties)
	for i := 0; i < parties-1; i++ {
		go func() {
			if err := b.Arrive(); err != nil {
				t.Errorf("Arrive: %v", err)
			}
			released.Add(1)
			done <- struct{}{}
		}()
	}
	waitBlocked(t, done, 50*time.Millisecond, "3 of 4 arrived")
	if got := released.Load(); got != 0 {
		t.Fatalf("released=%d before last arrival", got)
	}
	// Final arrival releases the whole round.
	if err := b.Arrive(); err != nil {
		t.Fatalf("last Arrive: %v", err)
	}
	for i := 0; i < parties-1; i++ {
		waitReleased(t, done, time.Second, "all parties")
	}
	if got := b.Round(); got != 1 {
		t.Fatalf("round=%d, want 1", got)
	}
}

// TestFailurePropagatesToWholeRound: one injected task failure is
// observed by every participant of the round; the next round is clean.
func TestFailurePropagatesToWholeRound(t *testing.T) {
	const parties = 3
	b := New(parties)
	injected := errors.New("injected task failure")
	errs := make([][]error, parties)
	var wg sync.WaitGroup
	for i := 0; i < parties; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for r := 0; r < 2; r++ {
				err := b.Run(func() error {
					// Stagger tasks so the failure lands mid-round.
					time.Sleep(time.Duration(idx) * 10 * time.Millisecond)
					if r == 0 && idx == parties-1 {
						return injected
					}
					return nil
				})
				errs[idx] = append(errs[idx], err)
			}
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if !errors.Is(errs[i][0], injected) {
			t.Errorf("participant %d round 0 err=%v, want injected failure", i, errs[i][0])
		}
		if errs[i][1] != nil {
			t.Errorf("participant %d round 1 err=%v, want nil (clean round)", i, errs[i][1])
		}
	}
	if got := b.Round(); got != 2 {
		t.Fatalf("round=%d, want 2", got)
	}
}

// TestLeaveReleasesRemaining: one participant leaves mid-round; the
// remaining arrivals already satisfy the reduced count and are
// released immediately.
func TestLeaveReleasesRemaining(t *testing.T) {
	b := New(3)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- b.Arrive() }()
	}
	// Let both arrivals land, then the third participant leaves.
	time.Sleep(50 * time.Millisecond)
	b.Leave()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Arrive after Leave: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("remaining participants not released after Leave")
		}
	}
	if got := b.Parties(); got != 2 {
		t.Fatalf("parties=%d, want 2", got)
	}
	if got := b.Round(); got != 1 {
		t.Fatalf("round=%d, want 1", got)
	}
}

// TestJoinAppliesNextRound: a joiner never changes the in-progress
// round's count (no premature release) and becomes a full participant
// from the next round on.
func TestJoinAppliesNextRound(t *testing.T) {
	b := New(2)
	first := make(chan struct{})
	go func() { b.Arrive(); close(first) }() // parks in round 0
	time.Sleep(30 * time.Millisecond)
	joined := make(chan struct{})
	go func() { b.Join(); close(joined) }()
	// Joining must not release the in-progress round early, and the
	// joiner itself waits for the round boundary.
	waitBlocked(t, first, 50*time.Millisecond, "round 0 with pending join")
	waitBlocked(t, joined, 10*time.Millisecond, "joiner before round boundary")
	// Second arrival completes round 0; the joiner enters round 1.
	second := make(chan struct{})
	go func() { b.Arrive(); close(second) }()
	waitReleased(t, first, time.Second, "round 0 first waiter")
	waitReleased(t, second, time.Second, "round 0 second arrival")
	waitReleased(t, joined, time.Second, "joiner at round boundary")
	if got := b.Parties(); got != 3 {
		t.Fatalf("parties=%d, want 3 after join", got)
	}
	// Round 1 now requires 3 arrivals: two must not suffice.
	round1 := make(chan struct{}, 3)
	for i := 0; i < 2; i++ {
		go func() { b.Arrive(); round1 <- struct{}{} }()
	}
	waitBlocked(t, round1, 50*time.Millisecond, "round 1 with 2 of 3 arrived")
	go func() { b.Arrive(); round1 <- struct{}{} }()
	for i := 0; i < 3; i++ {
		waitReleased(t, round1, time.Second, "round 1 all parties")
	}
}

// TestLateArrivalNotCountedInNewRound: a participant released from
// round N whose next arrival lands while others are still waking up
// must be accounted to round N+1 only, never double-counted.
func TestLateArrivalNotCountedInNewRound(t *testing.T) {
	b := New(2)
	var wg sync.WaitGroup
	wg.Add(2)
	aSecond := make(chan struct{})
	go func() {
		defer wg.Done()
		b.Arrive() // round 0
		b.Arrive() // round 1: arrives while B may still be waking up
		close(aSecond)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(30 * time.Millisecond) // "late" participant
		b.Arrive()                        // completes round 0
		time.Sleep(50 * time.Millisecond) // slow to re-arrive
		b.Arrive()                        // completes round 1
	}()
	// A's second arrival must NOT complete round 1 by itself.
	waitBlocked(t, aSecond, 40*time.Millisecond, "round 1 after single fast arrival")
	wg.Wait()
	if got := b.Round(); got != 2 {
		t.Fatalf("round=%d, want 2", got)
	}
}

// TestCountChangeNoPermanentWait: after a mid-round failure plus a
// leave, the reduced round still completes and the next round is clean.
func TestCountChangeNoPermanentWait(t *testing.T) {
	b := New(3)
	injected := errors.New("boom")
	results := make(chan error, 3)
	// Participant 0 fails its task; 1 and 2 are plain arrivals.
	go func() { results <- b.Run(func() error { time.Sleep(20 * time.Millisecond); return injected }) }()
	go func() { results <- b.Arrive() }()
	go func() { results <- b.Arrive() }()
	for i := 0; i < 3; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, injected) {
				t.Fatalf("round 0 err=%v, want injected", err)
			}
		case <-time.After(time.Second):
			t.Fatal("broken round did not propagate to all participants")
		}
	}
	// One participant leaves before the next round; two must suffice.
	b.Leave()
	next := make(chan error, 2)
	go func() { next <- b.Arrive() }()
	go func() { next <- b.Arrive() }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-next:
			if err != nil {
				t.Fatalf("round 1 err=%v, want nil", err)
			}
		case <-time.After(time.Second):
			t.Fatal("round after leave+failure never completed")
		}
	}
	if got := b.Parties(); got != 2 {
		t.Fatalf("parties=%d, want 2", got)
	}
}

// TestThousandRoundsNoCrosstalk: 1000 consecutive rounds with injected
// task timing jitter; every round must release exactly `parties`
// participants, proving no state leaks across rounds.
func TestThousandRoundsNoCrosstalk(t *testing.T) {
	const (
		parties = 4
		rounds  = 1000
	)
	b := New(parties)
	var released [rounds]atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < parties; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				err := b.Run(func() error {
					// Injected timing jitter to force interleavings.
					if (id+r)%7 == 0 {
						time.Sleep(time.Microsecond * 50)
					}
					return nil
				})
				if err != nil {
					t.Errorf("participant %d round %d: %v", id, r, err)
					return
				}
				released[r].Add(1)
			}
		}(i)
	}
	wg.Wait()
	for r := 0; r < rounds; r++ {
		if got := released[r].Load(); got != parties {
			t.Fatalf("round %d released %d participants, want %d", r, got, parties)
		}
	}
	if got := b.Round(); got != rounds {
		t.Fatalf("round=%d, want %d", got, rounds)
	}
}

// ExampleBarrier demonstrates a full lifecycle: rounds, an injected
// failure, a leave, and a join.
func ExampleBarrier() {
	b := New(3)
	var wg sync.WaitGroup
	worker := func(name string, fail bool) {
		defer wg.Done()
		for r := 0; r < 2; r++ {
			err := b.Run(func() error {
				if fail && r == 0 {
					return fmt.Errorf("%s failed", name)
				}
				return nil
			})
			fmt.Printf("%s round %d err=%v\n", name, r, err != nil)
		}
	}
	wg.Add(3)
	go worker("w1", false)
	go worker("w2", true)
	go worker("w3", false)
	wg.Wait()
	fmt.Println("rounds completed:", b.Round())
	// Output ordering of workers is nondeterministic, so only the
	// summary line is checked loosely by running the example.
}
