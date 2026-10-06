package agg

import (
	"math/rand"
	"testing"
)

func lastEvents(a *Aggregator, n int) []Event {
	log := a.Log()
	if len(log) < n {
		n = len(log)
	}
	return log[len(log)-n:]
}

func TestOutOfOrderBarrierAndFinalize(t *testing.T) {
	a := New()
	for _, p := range []string{"A", "B", "C"} {
		a.Join(p)
	}
	a.SubmitData("A", 0, 10)
	a.SubmitData("B", 0, 20)
	a.SubmitData("C", 0, 5)
	a.IssueReset()

	// Out-of-order arrivals are cached; no finalize until all arrive.
	a.BarrierAck("C", 0)
	a.BarrierAck("A", 0)
	if _, ok := a.State().Finals[0]; ok {
		t.Fatal("finalized before all partitions arrived")
	}
	// Post-barrier data buffers into the new generation.
	a.SubmitData("C", 1, 100)
	a.BarrierAck("B", 0)

	s := a.State()
	if s.Finals[0] != 35 {
		t.Fatalf("final gen0 = %d, want 35", s.Finals[0])
	}
	if s.CurrentGen != 1 || s.OldAcc != 100 {
		t.Fatalf("after roll: gen=%d oldAcc=%d, want gen=1 oldAcc=100", s.CurrentGen, s.OldAcc)
	}
	if vs := Verify(a.Log()); len(vs) != 0 {
		t.Fatalf("violations: %v", vs)
	}
}

func TestDuplicateBarrierIgnored(t *testing.T) {
	a := New()
	a.Join("A")
	a.IssueReset()
	a.BarrierAck("A", 0) // completes immediately (single member)
	before := len(a.Log())
	a.BarrierAck("A", 0) // duplicate / stale
	evs := lastEvents(a, 1)
	if evs[0].Kind != KindBarrierIgnored {
		t.Fatalf("duplicate ack produced %s, want BarrierIgnored", evs[0].Kind)
	}
	if len(a.Log()) != before+1 {
		t.Fatal("duplicate ack emitted extra events")
	}
	if len(a.State().Finals) != 1 {
		t.Fatalf("finals = %v, want exactly one", a.State().Finals)
	}
	if vs := Verify(a.Log()); len(vs) != 0 {
		t.Fatalf("violations: %v", vs)
	}
}

func TestPartitionExitShrinksBarrierSet(t *testing.T) {
	a := New()
	for _, p := range []string{"A", "B", "C"} {
		a.Join(p)
	}
	a.SubmitData("A", 0, 1)
	a.SubmitData("B", 0, 2)
	a.SubmitData("C", 0, 4)
	a.IssueReset()
	a.BarrierAck("A", 0)
	a.BarrierAck("B", 0)
	a.Exit("C") // completes the barrier set without C
	if got := a.State().Finals[0]; got != 7 {
		t.Fatalf("final gen0 = %d, want 7 (C's pre-exit data still counts)", got)
	}
	// Late arrivals from the exited partition are ignored.
	a.BarrierAck("C", 0)
	a.SubmitData("C", 1, 100)
	evs := lastEvents(a, 2)
	if evs[0].Kind != KindBarrierIgnored || evs[1].Kind != KindDataDropped {
		t.Fatalf("late events not ignored: %s, %s", evs[0].Kind, evs[1].Kind)
	}
	if vs := Verify(a.Log()); len(vs) != 0 {
		t.Fatalf("violations: %v", vs)
	}
}

func TestRecoveryEqualsLive(t *testing.T) {
	a := New()
	r := rand.New(rand.NewSource(7))
	parts := []string{"A", "B", "C", "D"}
	for _, p := range parts {
		a.Join(p)
	}
	for i := 0; i < 500; i++ {
		p := parts[r.Intn(len(parts))]
		switch r.Intn(5) {
		case 0:
			a.SubmitData(p, uint64(r.Intn(3)), int64(r.Intn(100)))
		case 1:
			a.IssueReset()
		case 2:
			a.BarrierAck(p, uint64(r.Intn(3)))
		case 3:
			a.Exit(p)
		case 4:
			a.Join(p)
		}
		// Recovery equivalence must hold at every single step.
		if Replay(a.Log()).Hash() != a.State().Hash() {
			t.Fatalf("step %d: replay diverges from live state", i)
		}
	}
	if vs := Verify(a.Log()); len(vs) != 0 {
		t.Fatalf("violations: %v", vs)
	}
}

func TestFuzzProtocolInvariants(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		a := New()
		r := rand.New(rand.NewSource(seed))
		parts := []string{"A", "B", "C", "D", "E"}
		for i := 0; i < 2000; i++ {
			p := parts[r.Intn(len(parts))]
			switch r.Intn(6) {
			case 0, 1:
				a.SubmitData(p, uint64(r.Intn(4)), int64(r.Intn(50)))
			case 2:
				a.IssueReset()
			case 3, 4:
				a.BarrierAck(p, uint64(r.Intn(4)))
			case 5:
				if r.Intn(2) == 0 {
					a.Exit(p)
				} else {
					a.Join(p)
				}
			}
		}
		if Replay(a.Log()).Hash() != a.State().Hash() {
			t.Fatalf("seed %d: replay diverges", seed)
		}
		if vs := Verify(a.Log()); len(vs) != 0 {
			t.Fatalf("seed %d: violations: %v", seed, vs)
		}
	}
}
