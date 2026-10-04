package barrier

import (
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTripAndReuse(t *testing.T) {
	const parties, rounds = 3, 5
	b := New(parties)

	type result struct {
		round  uint64
		reason Reason
	}
	results := make([][rounds]result, parties)

	var wg sync.WaitGroup
	for p := 0; p < parties; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				r, reason := b.Arrive(false)
				results[p][i] = result{r, reason}
			}
		}(p)
	}
	wg.Wait()

	for i := 0; i < rounds; i++ {
		for p := 0; p < parties; p++ {
			got := results[p][i]
			if got.round != uint64(i) || got.reason != ReasonTripped {
				t.Fatalf("participant %d round %d: got (%d, %v), want (%d, %v)",
					p, i, got.round, got.reason, i, ReasonTripped)
			}
		}
	}
	if round, _, _ := b.Snapshot(); round != rounds {
		t.Fatalf("current round = %d, want %d", round, rounds)
	}
}

func TestExitTakesEffectNextRound(t *testing.T) {
	b := New(3)

	done := make(chan struct{}, 3)
	// Participant A exits after round 0.
	go func() { b.Arrive(true); done <- struct{}{} }()
	// Participants B and C stay for two rounds.
	for i := 0; i < 2; i++ {
		go func() {
			b.Arrive(false)
			b.Arrive(false)
			done <- struct{}{}
		}()
	}

	waitFor(t, "round 1 to open", func() bool {
		round, _, _ := b.Snapshot()
		return round >= 1
	})
	if _, parties, _ := b.Snapshot(); parties != 2 {
		t.Fatalf("parties after exit = %d, want 2", parties)
	}

	// If the exit had not been applied, round 1 would wait for a third
	// arrival and time out below.
	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for participants")
		}
	}
}

func TestJoinOnlyFromNextRound(t *testing.T) {
	b := New(1)

	joined := make(chan uint64, 1)
	go func() { joined <- b.Join() }()

	// The joiner must not leak into the in-progress round.
	select {
	case r := <-joined:
		t.Fatalf("Join returned %d before the round settled", r)
	case <-time.After(50 * time.Millisecond):
	}
	if _, parties, _ := b.Snapshot(); parties != 1 {
		t.Fatalf("parties before settlement = %d, want 1", parties)
	}

	// Round 0 trips with the single original participant; the joiner
	// becomes active in round 1.
	if round, reason := b.Arrive(false); round != 0 || reason != ReasonTripped {
		t.Fatalf("round 0 = (%d, %v), want (0, %v)", round, reason, ReasonTripped)
	}
	select {
	case r := <-joined:
		if r != 1 {
			t.Fatalf("joiner active round = %d, want 1", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Join did not return after settlement")
	}
	if _, parties, _ := b.Snapshot(); parties != 2 {
		t.Fatalf("parties after join = %d, want 2", parties)
	}

	// Round 1 now needs both participants.
	go b.Arrive(false)
	if round, reason := b.Arrive(false); round != 1 || reason != ReasonTripped {
		t.Fatalf("round 1 = (%d, %v), want (1, %v)", round, reason, ReasonTripped)
	}
}

func TestCancelWakesAllWaiters(t *testing.T) {
	b := New(3)

	const waiters = 2
	type result struct {
		round  uint64
		reason Reason
	}
	results := make(chan result, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			r, reason := b.Arrive(false)
			results <- result{r, reason}
		}()
	}
	waitFor(t, "both waiters to arrive", func() bool {
		_, _, arrived := b.Snapshot()
		return arrived == waiters
	})

	if cancelled := b.Cancel(); cancelled != 0 {
		t.Fatalf("cancelled round = %d, want 0", cancelled)
	}
	for i := 0; i < waiters; i++ {
		select {
		case got := <-results:
			if got.round != 0 || got.reason != ReasonCancelled {
				t.Fatalf("waiter got (%d, %v), want (0, %v)", got.round, got.reason, ReasonCancelled)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiter not woken by Cancel")
		}
	}

	// The barrier stays reusable: round 1 still expects 3 arrivals.
	if round, parties, arrived := b.Snapshot(); round != 1 || parties != 3 || arrived != 0 {
		t.Fatalf("snapshot = (%d, %d, %d), want (1, 3, 0)", round, parties, arrived)
	}
}

// TestLastArrivalExitCancelRace hammers the interleaving where the last
// arrival (declaring exit) and a Cancel race on the same round. Exactly
// one exit must ever be accounted: parties must go 2 -> 1, never to 0.
func TestLastArrivalExitCancelRace(t *testing.T) {
	for i := 0; i < 1000; i++ {
		b := New(2)

		aDone := make(chan struct{})
		go func() { b.Arrive(false); close(aDone) }()
		waitFor(t, "first arrival", func() bool {
			_, _, arrived := b.Snapshot()
			return arrived == 1
		})

		bDone := make(chan struct{})
		go func() { b.Arrive(true); close(bDone) }() // last arrival + exit
		go b.Cancel()

		// If Cancel wins round 0, B's arrival lands in a later round and
		// blocks; keep cancelling empty rounds until B is released.
	releasing:
		for {
			select {
			case <-bDone:
				break releasing
			case <-time.After(time.Millisecond):
				b.Cancel()
			}
		}
		<-aDone

		if _, parties, _ := b.Snapshot(); parties != 1 {
			t.Fatalf("iteration %d: parties = %d, want 1 (exit accounted more than once or lost)", i, parties)
		}
	}
}

func TestNewPanicsOnNegativeParties(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New(-1) did not panic")
		}
	}()
	New(-1)
}
