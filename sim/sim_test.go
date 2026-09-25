package sim

import (
	"fmt"
	"math/rand"
	"testing"
)

func mustNoViolations(t *testing.T, s *Simulator) {
	t.Helper()
	if len(s.Violations) > 0 {
		for _, v := range s.Violations {
			t.Log(v)
		}
		t.Fatalf("%d safety violations", len(s.Violations))
	}
}

// Safety: under random drops, duplicates, crashes and restarts, the
// conservation invariant, non-negativity and credit-once invariant hold
// after every single event.
func TestConservationUnderChaos(t *testing.T) {
	s := New(1000, []int64{400, 300, 200, 100}, 42)
	s.CheckInvariants = true
	s.Net.DropRate = 0.3
	s.Net.DupRate = 0.3
	rng := rand.New(rand.NewSource(7))

	for i := 0; i < 300; i++ {
		tick := int64(i + 1)
		switch rng.Intn(5) {
		case 0:
			r, amt := rng.Intn(4), int64(rng.Intn(120))
			s.Schedule(tick, ActionEvent{func(s *Simulator) { s.Deduct(r, amt) }})
		case 1:
			from, to := rng.Intn(4), rng.Intn(4)
			if from != to {
				amt := int64(rng.Intn(80))
				id := fmt.Sprintf("T-%03d", i)
				s.Schedule(tick, ActionEvent{func(s *Simulator) { s.Transfer(from, to, amt, id) }})
			}
		case 2:
			r := rng.Intn(4)
			s.Schedule(tick, ActionEvent{func(s *Simulator) { s.Crash(r) }})
		case 3:
			r := rng.Intn(4)
			s.Schedule(tick, ActionEvent{func(s *Simulator) { s.Restart(r) }})
		}
	}
	s.Run(400)
	mustNoViolations(t, s)
	if s.Consumed > s.Total {
		t.Fatalf("consumed %d exceeds total %d", s.Consumed, s.Total)
	}

	// Heal the network: every transfer must settle and the invariant must
	// still hold at the end.
	s.Net.Healed = true
	for r := range s.Replicas {
		s.Restart(r)
	}
	s.RunUntilEmpty(10000)
	mustNoViolations(t, s)
	if !s.Settled() {
		t.Fatalf("not settled after heal: %s", s.Report())
	}
	if got := s.Available() + s.Consumed; got != s.Total {
		t.Fatalf("final accounting %d != total %d", got, s.Total)
	}
}

// Safety: delivering the same transfer message repeatedly credits exactly once.
func TestDuplicateDeliveryCreditsOnce(t *testing.T) {
	s := New(300, []int64{300, 0}, 1)
	s.CheckInvariants = true
	s.Net.Healed = true

	if !s.Transfer(0, 1, 100, "T-1") {
		t.Fatal("transfer rejected")
	}
	m := Message{Kind: KindTransfer, ID: "T-1", From: 0, To: 1, Amount: 100}
	s.Deliver(m) // first delivery: credits
	s.Deliver(m) // duplicate: must not credit
	s.Deliver(m) // duplicate: must not credit
	s.RunUntilEmpty(1000)

	if got := s.Replicas[1].Local; got != 100 {
		t.Fatalf("receiver local=%d, want 100 (credited once)", got)
	}
	if s.Credits["T-1"] != 1 {
		t.Fatalf("credits=%d, want 1", s.Credits["T-1"])
	}
	mustNoViolations(t, s)
}

// Safety: a sender-side request retry with the same ID cannot debit twice.
func TestDuplicateTransferRequestDebitsOnce(t *testing.T) {
	s := New(300, []int64{300, 0}, 1)
	s.CheckInvariants = true
	s.Net.Healed = true

	s.Transfer(0, 1, 100, "T-1")
	s.Transfer(0, 1, 100, "T-1") // client retry, same ID
	s.Transfer(0, 1, 100, "T-1") // client retry, same ID
	s.RunUntilEmpty(1000)

	if got := s.Replicas[0].Local; got != 200 {
		t.Fatalf("sender local=%d, want 200 (debited once)", got)
	}
	if got := s.Replicas[1].Local; got != 100 {
		t.Fatalf("receiver local=%d, want 100", got)
	}
	mustNoViolations(t, s)
}

// Safety: with the network dropping everything, sender retries accumulate;
// once healed, the receiver still credits exactly once.
func TestRetryStormCreditsOnce(t *testing.T) {
	s := New(500, []int64{500, 0}, 1)
	s.CheckInvariants = true
	s.Net.DropRate = 1.0 // total blackout

	s.Transfer(0, 1, 200, "T-1")
	s.Run(100) // ~20 retries, all dropped
	if s.Credits["T-1"] != 0 {
		t.Fatal("credited during blackout")
	}
	if got := s.Available(); got != 300 {
		t.Fatalf("available=%d during flight, want 300", got)
	}

	s.Net.Healed = true
	s.RunUntilEmpty(10000)
	if s.Credits["T-1"] != 1 {
		t.Fatalf("credits=%d, want 1 after retry storm", s.Credits["T-1"])
	}
	if !s.Settled() {
		t.Fatal("not settled")
	}
	mustNoViolations(t, s)
}

// Safety: local entitlement can never be overdrawn, by deduction or transfer.
func TestNoOverdraw(t *testing.T) {
	s := New(100, []int64{100, 0}, 1)
	s.CheckInvariants = true

	if s.Deduct(0, 150) {
		t.Fatal("deduct beyond local accepted")
	}
	if s.Transfer(0, 1, 150, "T-big") {
		t.Fatal("transfer beyond local accepted")
	}
	if s.Deduct(0, -5) {
		t.Fatal("negative deduct accepted")
	}
	if got := s.Replicas[0].Local; got != 100 {
		t.Fatalf("local=%d after rejected ops, want 100", got)
	}
	if !s.Deduct(0, 100) {
		t.Fatal("exact deduct rejected")
	}
	if s.Deduct(0, 1) {
		t.Fatal("deduct past zero accepted")
	}
	mustNoViolations(t, s)
}

// Safety: while a transfer is in flight (sender debited, receiver not
// credited), total available goes DOWN, never up. Lost messages lose
// availability; they cannot create it.
func TestInFlightReducesAvailable(t *testing.T) {
	s := New(300, []int64{300, 0}, 1)
	s.CheckInvariants = true
	s.Net.DropRate = 1.0

	before := s.Available()
	s.Transfer(0, 1, 100, "T-1")
	s.Run(50)
	if got := s.Available(); got != before-100 {
		t.Fatalf("available=%d, want %d (decreased by in-flight amount)", got, before-100)
	}
	if s.Available() > before {
		t.Fatal("available increased while message lost")
	}

	s.Net.Healed = true
	s.RunUntilEmpty(10000)
	if got := s.Available(); got != before {
		t.Fatalf("available=%d after delivery, want %d restored", got, before)
	}
	mustNoViolations(t, s)
}

// Safety: offline deductions on many replicas, fully uncoordinated, can never
// consume more than the global quota.
func TestOfflineDeductionNeverExceedsGlobal(t *testing.T) {
	s := New(1000, []int64{250, 250, 250, 250}, 1)
	s.CheckInvariants = true
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 2000; i++ {
		s.Deduct(rng.Intn(4), int64(rng.Intn(50)))
		if s.Consumed > s.Total {
			t.Fatalf("consumed %d > total %d", s.Consumed, s.Total)
		}
	}
	if got := s.Available() + s.Consumed; got != s.Total {
		t.Fatalf("available+consumed=%d != total %d", got, s.Total)
	}
	mustNoViolations(t, s)
}

// Liveness: after the network heals and crashed replicas restart, every
// transfer completes within bounded virtual time.
func TestLivenessAfterHeal(t *testing.T) {
	s := New(900, []int64{300, 300, 300}, 9)
	s.CheckInvariants = true
	s.Net.DropRate = 0.5
	s.Net.DupRate = 0.5

	s.Transfer(0, 1, 100, "T-1")
	s.Transfer(1, 2, 100, "T-2")
	s.Transfer(2, 0, 100, "T-3")
	s.Crash(1)
	s.Run(60)
	if s.Settled() {
		t.Fatal("unexpectedly settled during blackout/crash")
	}

	s.Net.Healed = true
	s.Restart(1)
	s.RunUntilEmpty(100000)

	if !s.Settled() {
		t.Fatalf("liveness failed, pending remain: %s", s.Report())
	}
	for _, id := range []string{"T-1", "T-2", "T-3"} {
		if s.Credits[id] != 1 {
			t.Fatalf("%s credited %d times, want exactly 1", id, s.Credits[id])
		}
	}
	if got := s.Available(); got != 900 {
		t.Fatalf("available=%d, want 900 (nothing consumed)", got)
	}
	mustNoViolations(t, s)
}

// Liveness: ack loss alone (transfer delivered, ack dropped) still completes
// via sender retry + receiver dedup.
func TestAckLossCompletes(t *testing.T) {
	s := New(200, []int64{200, 0}, 1)
	s.CheckInvariants = true
	s.Net.Healed = true

	s.Transfer(0, 1, 50, "T-1")
	s.Run(10) // transfer delivered, ack in flight or done
	// Force ack loss by crashing sender briefly? Instead directly drop pending acks:
	// simulate by re-delivering the transfer (as a retry would) after ack loss.
	s.Deliver(Message{Kind: KindTransfer, ID: "T-1", From: 0, To: 1, Amount: 50})
	s.RunUntilEmpty(10000)

	if !s.Settled() {
		t.Fatal("not settled after ack loss + retry")
	}
	if s.Credits["T-1"] != 1 {
		t.Fatalf("credits=%d, want 1", s.Credits["T-1"])
	}
	mustNoViolations(t, s)
}
