package sim

import (
	"math/rand"
	"testing"
)

func mustAudit(t *testing.T, s *Simulator) {
	t.Helper()
	if err := s.Audit(); err != nil {
		t.Fatalf("safety invariant violated: %v", err)
	}
}

func TestSplitAndOfflineSpend(t *testing.T) {
	s := NewSimulator(Perfect{D: 1})
	r1 := s.AddReplica(1, 100)
	r2 := s.AddReplica(2, 50)
	r3 := s.AddReplica(3, 50) // global quota Q = 200
	s.Run()

	if err := r1.Spend(30); err != nil {
		t.Fatalf("spend: %v", err)
	}
	if err := r2.Spend(51); err != ErrOverdraft {
		t.Fatalf("want ErrOverdraft, got %v", err)
	}
	if err := r2.Spend(0); err != ErrInvalidAmount {
		t.Fatalf("want ErrInvalidAmount, got %v", err)
	}
	if err := r2.Spend(50); err != nil {
		t.Fatalf("spend all local credit: %v", err)
	}
	s.Run()
	mustAudit(t, s)

	snap := s.SnapshotNow()
	if snap.TotalAvailable() != 120 {
		t.Fatalf("available = %d, want 120", snap.TotalAvailable())
	}
	if snap.Spent != 80 || snap.TotalAvailable()+snap.Spent != 200 {
		t.Fatalf("offline deduction escaped the global quota: avail=%d spent=%d",
			snap.TotalAvailable(), snap.Spent)
	}
	if r2.Balance() != 0 || r2.Spent() != 50 {
		t.Fatalf("R2 = bal %d spent %d, want 0/50", r2.Balance(), r2.Spent())
	}
	if r3.Balance() != 50 {
		t.Fatalf("R3 balance = %d, want 50", r3.Balance())
	}
}

func TestTransferExactlyOnceUnderDuplication(t *testing.T) {
	// The network duplicates both the transfer and (via the receiver's
	// re-ack of the duplicate) acknowledgements. Credit must land once.
	f := ScriptedFaults{
		Rand: rand.New(rand.NewSource(1)),
		MinD: 1,
		ForceDup: map[string]bool{
			FaultKey(Message{Kind: MsgTransfer, ID: TransferID{1, 1}, Sender: 1, Target: 2, SentAt: 0}): true,
		},
	}
	s := NewSimulator(f)
	r1 := s.AddReplica(1, 100)
	r2 := s.AddReplica(2, 0)
	id, err := r1.Transfer(2, 30)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	s.Run()
	mustAudit(t, s)

	if r1.Balance() != 70 || r2.Balance() != 30 {
		t.Fatalf("balances = %d/%d, want 70/30", r1.Balance(), r2.Balance())
	}
	if r1.PendingCount() != 0 {
		t.Fatalf("transfer %v not completed", id)
	}
	st := s.Stats()
	if st.TransferDups < 1 {
		t.Fatalf("expected duplicate transfer copies to be rejected, got 0")
	}
}

func TestInFlightAvailabilityAndNoOverdraft(t *testing.T) {
	// First send is lost; the retransmission timer recovers it.
	key := FaultKey(Message{Kind: MsgTransfer, ID: TransferID{1, 1}, Sender: 1, Target: 2, SentAt: 0})
	f := ScriptedFaults{Rand: rand.New(rand.NewSource(7)), MinD: 1, ForceDrop: map[string]bool{key: true}}
	s := NewSimulator(f)
	r1 := s.AddReplica(1, 100)
	r2 := s.AddReplica(2, 0)
	r1.ResendInterval = 20
	if _, err := r1.Transfer(2, 20); err != nil {
		t.Fatalf("transfer: %v", err)
	}

	// While deducted-but-not-received, total available must be 80 and the
	// escrowed 20 can neither be spent nor transferred again.
	s.At(10, func(int64) {
		snap := s.SnapshotNow()
		if snap.TotalAvailable() != 80 {
			t.Fatalf("in-flight available = %d, want 80", snap.TotalAvailable())
		}
		if r1.Balance() != 80 || r1.InFlight() != 20 {
			t.Fatalf("R1 = bal %d escrow %d, want 80/20", r1.Balance(), r1.InFlight())
		}
		if err := r1.Spend(81); err != ErrOverdraft {
			t.Fatalf("escrow must not be spendable, got %v", err)
		}
		if _, err := r1.Transfer(2, 81); err != ErrOverdraft {
			t.Fatalf("escrow must not be transferable, got %v", err)
		}
	})
	s.Run()
	mustAudit(t, s)

	if r1.Balance() != 80 || r2.Balance() != 20 {
		t.Fatalf("balances = %d/%d, want 80/20", r1.Balance(), r2.Balance())
	}
	if r1.PendingCount() != 0 {
		t.Fatal("transfer never completed")
	}
	if snap := s.SnapshotNow(); snap.TotalAvailable() != 100 {
		t.Fatalf("available after completion = %d, want 100", snap.TotalAvailable())
	}
}

func TestSenderCrashAndRestartCompletesTransfer(t *testing.T) {
	key := FaultKey(Message{Kind: MsgTransfer, ID: TransferID{1, 1}, Sender: 1, Target: 2, SentAt: 0})
	f := ScriptedFaults{Rand: rand.New(rand.NewSource(3)), MinD: 1, ForceDrop: map[string]bool{key: true}}
	s := NewSimulator(f)
	r1 := s.AddReplica(1, 100)
	r2 := s.AddReplica(2, 0)
	r1.ResendInterval = 20
	if _, err := r1.Transfer(2, 40); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	s.At(5, func(int64) { s.Crash(1) })
	s.At(8, func(int64) {
		if err := r1.Spend(1); err != ErrReplicaDown {
			t.Fatalf("down replica spend: want ErrReplicaDown, got %v", err)
		}
	})
	s.At(12, func(int64) { s.Restart(1) })
	s.Run()
	mustAudit(t, s)

	if r1.Balance() != 60 || r2.Balance() != 40 || r1.PendingCount() != 0 {
		t.Fatalf("after restart: R1 bal %d pending %d, R2 bal %d; want 60/0/40",
			r1.Balance(), r1.PendingCount(), r2.Balance())
	}
}

func TestReceiverReplayAfterCrashCreditsOnce(t *testing.T) {
	// The first transfer copy is durably credited, but its ack is lost.
	// The receiver then crashes and restarts. The sender's timeout-driven
	// retry carries the same unique id and must be rejected by the durable
	// dedup key recovered from the WAL: credit lands exactly once.
	ackKey := FaultKey(Message{Kind: MsgAck, ID: TransferID{1, 1}, Sender: 2, Target: 1, SentAt: 1})
	f := ScriptedFaults{Rand: rand.New(rand.NewSource(5)), MinD: 1, ForceDrop: map[string]bool{ackKey: true}}
	s := NewSimulator(f)
	r1 := s.AddReplica(1, 100)
	r2 := s.AddReplica(2, 0)
	r1.ResendInterval = 20
	if _, err := r1.Transfer(2, 30); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	s.At(5, func(int64) { s.Crash(2) }) // after the durable credit at t=1
	s.At(10, func(int64) { s.Restart(2) })
	s.Run()
	mustAudit(t, s)

	if r2.Balance() != 30 {
		t.Fatalf("R2 balance = %d, want exactly 30 (no replay credit)", r2.Balance())
	}
	if r1.Balance() != 70 || r1.PendingCount() != 0 {
		t.Fatalf("R1 balance = %d pending = %d, want 70/0", r1.Balance(), r1.PendingCount())
	}
	if s.Stats().TransferDups < 1 {
		t.Fatal("expected the re-delivered transfer to be rejected by the durable dedup key")
	}
}

func TestReceiverCrashKeepsDurableBalance(t *testing.T) {
	s := NewSimulator(Perfect{D: 1})
	r1 := s.AddReplica(1, 100)
	r2 := s.AddReplica(2, 0)
	if _, err := r1.Transfer(2, 30); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	s.Run()
	s.Crash(2)
	s.Restart(2)
	if r2.Balance() != 30 || r2.Spent() != 0 {
		t.Fatalf("recovered R2 = bal %d spent %d, want 30/0", r2.Balance(), r2.Spent())
	}
	mustAudit(t, s)
}

func TestChaosConservation(t *testing.T) {
	for seed := int64(0); seed < 25; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := NewSimulator(RandomFaults{Rand: rng, MinD: 1, MaxD: 4, DropProb: 0.35, DuplicateProb: 0.2})
		reps := []*Replica{
			s.AddReplica(1, 250),
			s.AddReplica(2, 250),
			s.AddReplica(3, 250),
			s.AddReplica(4, 250),
		}
		for _, r := range reps {
			r.ResendInterval = 5
		}
		// Random workload of offline spends and transfers, including
		// deliberately oversized amounts to exercise overdraft rejection.
		for _, at := range []int64{3, 7, 11, 16, 22, 28, 34, 41, 48, 55} {
			tm := at
			s.At(tm, func(int64) {
				r := reps[rng.Intn(len(reps))]
				if r.Down() {
					return
				}
				amt := rng.Int63n(60) + 1
				if rng.Intn(2) == 0 {
					_ = r.Spend(amt)
				} else {
					target := reps[rng.Intn(len(reps))]
					_, _ = r.Transfer(target.ID(), amt)
				}
			})
		}
		// Crash/restart windows for different replicas.
		s.At(20, func(int64) { s.Crash(1) })
		s.At(45, func(int64) { s.Restart(1) })
		s.At(60, func(int64) { s.Crash(2) })
		s.At(90, func(int64) { s.Restart(2) })
		s.At(70, func(int64) { s.Crash(3) })
		s.At(120, func(int64) { s.Restart(3) })
		for _, at := range []int64{10, 30, 50, 80, 110, 140} {
			tm := at
			s.At(tm, func(int64) { mustAudit(t, s) })
		}
		// Everything is back up well before the queue drains.
		s.At(200, func(int64) {
			for _, r := range reps {
				if r.Down() {
					s.Restart(r.ID())
				}
			}
		})
		s.Run()
		mustAudit(t, s)
		for _, r := range reps {
			if r.PendingCount() != 0 {
				t.Fatalf("seed %d: R%d has %d stuck pending transfers", seed, r.ID(), r.PendingCount())
			}
			if r.Balance() < 0 {
				t.Fatalf("seed %d: negative balance", seed)
			}
		}
		snap := s.SnapshotNow()
		if snap.TotalAvailable() < 0 || snap.TotalAvailable() > snap.Quota {
			t.Fatalf("seed %d: available %d outside [0,%d]", seed, snap.TotalAvailable(), snap.Quota)
		}
		if snap.TotalAvailable()+snap.Spent != snap.Quota {
			t.Fatalf("seed %d: drained network but avail %d + spent %d != %d",
				seed, snap.TotalAvailable(), snap.Spent, snap.Quota)
		}
	}
}

func TestDeterminism(t *testing.T) {
	run := func() Snapshot {
		rng := rand.New(rand.NewSource(42))
		s := NewSimulator(RandomFaults{Rand: rng, MinD: 1, MaxD: 3, DropProb: 0.3, DuplicateProb: 0.15})
		a := s.AddReplica(1, 100)
		b := s.AddReplica(2, 100)
		a.ResendInterval, b.ResendInterval = 5, 5
		s.At(2, func(int64) { _, _ = a.Transfer(2, 40) })
		s.At(7, func(int64) { _ = b.Spend(25) })
		s.At(12, func(int64) { _, _ = b.Transfer(1, 30) })
		s.At(20, func(int64) { s.Crash(1) })
		s.At(30, func(int64) { s.Restart(1) })
		s.Run()
		if err := s.Audit(); err != nil {
			t.Fatal(err)
		}
		return s.SnapshotNow()
	}
	x, y := run(), run()
	if x.Balance != y.Balance || x.Spent != y.Spent || x.InFlight != y.InFlight {
		t.Fatalf("identical seeds produced different results: %+v vs %+v", x, y)
	}
	for i := range x.Replicas {
		if x.Replicas[i] != y.Replicas[i] {
			t.Fatalf("replica views differ: %+v vs %+v", x.Replicas[i], y.Replicas[i])
		}
	}
}
