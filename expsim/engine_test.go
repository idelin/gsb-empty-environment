package expsim

import "testing"

func bounds() Boundaries {
	return Boundaries{
		Upper: []int64{10, 10, 10, 10, 10},
		Lower: []int64{-10, -10, -10, -10, -10},
	}
}

func TestAccumulatesAndStopsAtFirstTouch(t *testing.T) {
	e := NewEngine(bounds())
	e.Process(
		Observation{ID: "o1", Time: 1, Seq: 1, Value: 3},
		Observation{ID: "o2", Time: 2, Seq: 1, Value: 4},
		Observation{ID: "o3", Time: 3, Seq: 1, Value: 5},
	)
	if !e.Stopped() || e.Decision() != DecisionUpper {
		t.Fatalf("want stop at upper, got stopped=%v decision=%s", e.Stopped(), e.Decision())
	}
	ev := e.Evidence()
	if ev.Step != 3 || ev.Sum != 12 || ev.ObsID != "o3" {
		t.Fatalf("bad evidence: %v", ev)
	}
}

func TestFrozenConclusionAfterStop(t *testing.T) {
	e := NewEngine(bounds())
	e.Process(
		Observation{ID: "o1", Time: 1, Seq: 1, Value: 3},
		Observation{ID: "o2", Time: 2, Seq: 1, Value: 4},
		Observation{ID: "o3", Time: 3, Seq: 1, Value: 5},
	)
	sumBefore, evBefore := e.Sum(), *e.Evidence()
	e.Process(
		Observation{ID: "o4", Time: 4, Seq: 1, Value: -100},
		Observation{ID: "o5", Time: 5, Seq: 1, Value: 100},
	)
	if e.Sum() != sumBefore || *e.Evidence() != evBefore || e.Decision() != DecisionUpper {
		t.Fatalf("post-stop data changed the conclusion: sum=%d ev=%v", e.Sum(), e.Evidence())
	}
	n, _, ignored := e.Counts()
	if n != 3 || ignored != 2 {
		t.Fatalf("counts: processed=%d ignored=%d, want 3/2", n, ignored)
	}
}

func TestSameTimeOrderedBySeq(t *testing.T) {
	b := Boundaries{Upper: []int64{10, 8, 10}, Lower: []int64{-10, -10, -10}}
	e := NewEngine(b)
	// Delivered out of order; seq must win: oB(seq1,+2) then oA(seq2,+6).
	e.Process(
		Observation{ID: "o1", Time: 1, Seq: 1, Value: 3},
		Observation{ID: "oA", Time: 2, Seq: 2, Value: 6},
		Observation{ID: "oB", Time: 2, Seq: 1, Value: 2},
	)
	// Seq order gives sums 3,5,11 -> stop at step 3 on upper=10.
	// Arrival order would give 3,9 (touch upper=8 at step 2).
	if !e.Stopped() || e.Evidence().Step != 3 || e.Evidence().Sum != 11 {
		t.Fatalf("seq ordering violated: stopped=%v ev=%v", e.Stopped(), e.Evidence())
	}
}

func TestRepeatedEventsAreIdempotent(t *testing.T) {
	e := NewEngine(bounds())
	o := Observation{ID: "o1", Time: 1, Seq: 1, Value: 3}
	e.Process(o, o, o)
	e.Process(o)
	n, dups, _ := e.Counts()
	if n != 1 || dups != 3 || e.Sum() != 3 {
		t.Fatalf("repeated events miscounted: n=%d dups=%d sum=%d", n, dups, e.Sum())
	}
}

func TestConflictingRepeatReported(t *testing.T) {
	e := NewEngine(bounds())
	e.Process(Observation{ID: "o1", Time: 1, Seq: 1, Value: 3})
	conflicts := e.Process(Observation{ID: "o1", Time: 1, Seq: 1, Value: 9})
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %d", len(conflicts))
	}
	if e.Sum() != 3 {
		t.Fatalf("conflicting redelivery changed the statistic: sum=%d", e.Sum())
	}
}

func TestLowerBoundary(t *testing.T) {
	e := NewEngine(bounds())
	e.Process(
		Observation{ID: "o1", Time: 1, Seq: 1, Value: -4},
		Observation{ID: "o2", Time: 2, Seq: 1, Value: -8},
	)
	if !e.Stopped() || e.Decision() != DecisionLower || e.Evidence().Step != 2 {
		t.Fatalf("want lower stop at step 2, got %v %v", e.Decision(), e.Evidence())
	}
}
