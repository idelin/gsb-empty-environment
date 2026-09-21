package rendezvous_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/roundrendezvous/rendezvous"
)

// waitFor polls cond until it holds, failing the test after a generous
// deadline. It keeps the tests deterministic without fixed sleeps.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func arriveAsync(b *rendezvous.Barrier, ctx context.Context) chan error {
	ch := make(chan error, 1)
	go func() { ch <- b.Arrive(ctx) }()
	return ch
}

func TestBasicRendezvous(t *testing.T) {
	const parties = 5
	var runs atomic.Int64
	b := rendezvous.New(parties, func(context.Context) error {
		runs.Add(1)
		return nil
	})

	errs := make([]chan error, parties)
	for i := range errs {
		errs[i] = arriveAsync(b, context.Background())
	}
	for _, ch := range errs {
		if err := <-ch; err != nil {
			t.Fatalf("Arrive returned %v, want nil", err)
		}
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("action ran %d times, want 1", got)
	}
	if got := b.Generation(); got != 1 {
		t.Fatalf("generation = %d, want 1", got)
	}
	if got := b.Arrived(); got != 0 {
		t.Fatalf("arrived = %d after release, want 0", got)
	}
}

func TestThousandRoundsNoCrosstalk(t *testing.T) {
	const parties = 8
	const rounds = 1000
	var runs atomic.Int64
	b := rendezvous.New(parties, func(context.Context) error {
		runs.Add(1)
		return nil
	})

	var wg sync.WaitGroup
	errCh := make(chan error, parties)
	for i := 0; i < parties; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				if err := b.Arrive(context.Background()); err != nil {
					errCh <- fmt.Errorf("round %d: %w", r, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := runs.Load(); got != rounds {
		t.Fatalf("action ran %d times, want %d (state leaked across rounds)", got, rounds)
	}
	if got := b.Generation(); got != rounds {
		t.Fatalf("generation = %d, want %d", got, rounds)
	}
	if got := b.Parties(); got != parties {
		t.Fatalf("parties = %d, want %d", got, parties)
	}
	if got := b.Arrived(); got != 0 {
		t.Fatalf("arrived = %d, want 0", got)
	}
}

func TestActionFailurePropagatesToWholeRound(t *testing.T) {
	const parties = 4
	const rounds = 5
	const failRound = 3 // 1-based action invocation that fails
	injected := errors.New("injected action failure")

	var calls atomic.Int64
	b := rendezvous.New(parties, func(context.Context) error {
		if calls.Add(1) == failRound {
			return injected
		}
		return nil
	})

	errs := make([][]error, parties)
	var wg sync.WaitGroup
	for i := range errs {
		errs[i] = make([]error, rounds)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				errs[i][r] = b.Arrive(context.Background())
			}
		}(i)
	}
	wg.Wait()

	for i := range errs {
		for r, err := range errs[i] {
			if r+1 == failRound {
				if !errors.Is(err, injected) {
					t.Errorf("participant %d round %d: got %v, want injected failure", i, r+1, err)
				}
			} else if err != nil {
				t.Errorf("participant %d round %d: got %v, want nil (failure leaked across rounds)", i, r+1, err)
			}
		}
	}
	if got := calls.Load(); got != rounds {
		t.Fatalf("action ran %d times, want %d", got, rounds)
	}
}

func TestFailReleasesAllWaiters(t *testing.T) {
	injected := errors.New("injected abort")
	b := rendezvous.New(3, nil)

	errs := make([]chan error, 2)
	for i := range errs {
		errs[i] = arriveAsync(b, context.Background())
	}
	waitFor(t, "2 arrivals", func() bool { return b.Arrived() == 2 })

	if !b.Fail(injected) {
		t.Fatal("Fail returned false on an open round")
	}
	for _, ch := range errs {
		if err := <-ch; !errors.Is(err, injected) {
			t.Fatalf("waiter got %v, want injected abort", err)
		}
	}

	// The failure must not poison the next round.
	next := make([]chan error, 3)
	for i := range next {
		next[i] = arriveAsync(b, context.Background())
	}
	for _, ch := range next {
		if err := <-ch; err != nil {
			t.Fatalf("next round got %v, want nil", err)
		}
	}
	if got := b.Generation(); got != 2 {
		t.Fatalf("generation = %d, want 2", got)
	}
}

func TestLeaveReleasesRemaining(t *testing.T) {
	var runs atomic.Int64
	b := rendezvous.New(3, func(context.Context) error {
		runs.Add(1)
		return nil
	})

	errs := make([]chan error, 2)
	for i := range errs {
		errs[i] = arriveAsync(b, context.Background())
	}
	waitFor(t, "2 arrivals", func() bool { return b.Arrived() == 2 })

	// The third participant exits before arriving: the remaining two already
	// satisfy the reduced requirement and must be released, not wait forever.
	b.Leave()

	for _, ch := range errs {
		if err := <-ch; err != nil {
			t.Fatalf("waiter got %v, want nil", err)
		}
	}
	if got := b.Parties(); got != 2 {
		t.Fatalf("parties = %d, want 2", got)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("action ran %d times, want 1", got)
	}
}

func TestCancelledArrivalExitsAndReleases(t *testing.T) {
	b := rendezvous.New(3, nil)

	ctx, cancel := context.WithCancel(context.Background())
	errA := arriveAsync(b, ctx)
	waitFor(t, "1 arrival", func() bool { return b.Arrived() == 1 })

	errB := arriveAsync(b, context.Background())
	waitFor(t, "2 arrivals", func() bool { return b.Arrived() == 2 })

	// A's context is cancelled: A leaves the barrier, shrinking the round
	// from 3 to 2 expected participants.
	cancel()
	if err := <-errA; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Arrive returned %v, want context.Canceled", err)
	}
	waitFor(t, "parties to shrink to 2", func() bool { return b.Parties() == 2 })
	waitFor(t, "arrived to drop to 1", func() bool { return b.Arrived() == 1 })

	// B is still waiting; one more arrival completes the reduced round.
	errC := arriveAsync(b, context.Background())
	for _, ch := range []chan error{errB, errC} {
		if err := <-ch; err != nil {
			t.Fatalf("waiter got %v, want nil", err)
		}
	}
}

func TestCancelDuringCompletingKeepsRoundIntact(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var calls atomic.Int64
	b := rendezvous.New(2, func(context.Context) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-unblock // test injects timing: hold the round open
		}
		return nil
	})

	ctxA, cancelA := context.WithCancel(context.Background())
	errA := arriveAsync(b, ctxA)
	waitFor(t, "1 arrival", func() bool { return b.Arrived() == 1 })

	errB := arriveAsync(b, context.Background())
	<-entered // round is completing inside the action

	// A cancels while its round is completing: A exits (shrinking future
	// rounds) but the in-flight round must still complete normally.
	cancelA()
	if err := <-errA; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Arrive returned %v, want context.Canceled", err)
	}
	close(unblock)
	if err := <-errB; err != nil {
		t.Fatalf("completing round got %v, want nil", err)
	}

	waitFor(t, "parties to shrink to 1", func() bool { return b.Parties() == 1 })
	// Round 2 expects only B now.
	if err := b.Arrive(context.Background()); err != nil {
		t.Fatalf("round 2 got %v, want nil", err)
	}
	if got := b.Generation(); got != 2 {
		t.Fatalf("generation = %d, want 2", got)
	}
}

func TestJoinTakesEffectNextRound(t *testing.T) {
	b := rendezvous.New(2, nil)
	b.Add(1) // joins from the next round on

	if got := b.Parties(); got != 2 {
		t.Fatalf("parties = %d, want 2: join must not change the current round", got)
	}

	// Round 1 still completes with the original 2 arrivals.
	round1 := []chan error{arriveAsync(b, context.Background()), arriveAsync(b, context.Background())}
	for _, ch := range round1 {
		if err := <-ch; err != nil {
			t.Fatalf("round 1 got %v, want nil", err)
		}
	}
	waitFor(t, "parties to grow to 3", func() bool { return b.Parties() == 3 })

	// Round 2: two arrivals must NOT release early.
	err1 := arriveAsync(b, context.Background())
	err2 := arriveAsync(b, context.Background())
	waitFor(t, "2 arrivals", func() bool { return b.Arrived() == 2 })
	select {
	case err := <-err1:
		t.Fatalf("premature release with 2 of 3 arrivals: %v", err)
	case err := <-err2:
		t.Fatalf("premature release with 2 of 3 arrivals: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// The third (newly joined) participant releases the round.
	err3 := arriveAsync(b, context.Background())
	for _, ch := range []chan error{err1, err2, err3} {
		if err := <-ch; err != nil {
			t.Fatalf("round 2 got %v, want nil", err)
		}
	}
}

func TestLateArrivalNotCountedInNewRound(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var calls atomic.Int64
	b := rendezvous.New(2, func(context.Context) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-unblock // hold round 1 open while a late arrival shows up
		}
		return nil
	})

	errA := arriveAsync(b, context.Background())
	errB := arriveAsync(b, context.Background())
	<-entered // round 1 is completing inside the action

	// D arrives while round 1 is finishing: it must wait for round 2 and
	// must not be counted toward (or released with) round 1.
	errD := arriveAsync(b, context.Background())
	select {
	case err := <-errD:
		t.Fatalf("late arrival released with finishing round: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// A late Fail must not disturb a round that is already completing.
	if b.Fail(errors.New("late abort")) {
		t.Fatal("Fail won against a completing round")
	}

	close(unblock)
	for _, ch := range []chan error{errA, errB} {
		if err := <-ch; err != nil {
			t.Fatalf("round 1 got %v, want nil", err)
		}
	}
	waitFor(t, "late arrival counted in round 2", func() bool {
		return b.Generation() == 1 && b.Arrived() == 1
	})

	// One more arrival completes round 2 with exactly 2 parties: the late
	// arrival consumed one slot, nothing leaked from round 1.
	errA2 := arriveAsync(b, context.Background())
	for _, ch := range []chan error{errD, errA2} {
		if err := <-ch; err != nil {
			t.Fatalf("round 2 got %v, want nil", err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("action ran %d times, want 2", got)
	}
	if got := b.Generation(); got != 2 {
		t.Fatalf("generation = %d, want 2", got)
	}
}

func TestRejoinAfterAllLeft(t *testing.T) {
	b := rendezvous.New(1, nil)
	if err := b.Arrive(context.Background()); err != nil {
		t.Fatalf("round 1 got %v, want nil", err)
	}
	b.Leave() // drains the barrier to zero participants
	if got := b.Parties(); got != 0 {
		t.Fatalf("parties = %d, want 0", got)
	}

	b.Add(2) // applies immediately: no round is in progress to join "next"
	if got := b.Parties(); got != 2 {
		t.Fatalf("parties = %d, want 2", got)
	}
	errs := []chan error{arriveAsync(b, context.Background()), arriveAsync(b, context.Background())}
	for _, ch := range errs {
		if err := <-ch; err != nil {
			t.Fatalf("round 2 got %v, want nil", err)
		}
	}
}

func ExampleBarrier() {
	var n atomic.Int64
	b := rendezvous.New(3, func(context.Context) error {
		fmt.Printf("round %d released\n", n.Add(1))
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 2; r++ {
				_ = b.Arrive(context.Background())
			}
		}()
	}
	wg.Wait()

	// Output:
	// round 1 released
	// round 2 released
}
