package expsim

import "testing"

func baseFacts() []Observation {
	return []Observation{
		{ID: "o1", Time: 1, Seq: 1, Value: 3},
		{ID: "o2", Time: 2, Seq: 1, Value: 4},
		{ID: "o3", Time: 3, Seq: 1, Value: 5}, // v0: sum 12 -> stop step 3
	}
}

func TestRevisionRecomputesStopPoint(t *testing.T) {
	fs := NewFactStore(baseFacts())
	c0 := Recompute(mustFacts(t, fs, 0), bounds(), 0)
	if c0.StopStep != 3 || c0.Decision != DecisionUpper {
		t.Fatalf("base conclusion: %+v", c0)
	}
	v, err := fs.Revise(Revision{ObsID: "o3", Value: 1, Note: "late instrumentation fix"})
	if err != nil {
		t.Fatal(err)
	}
	c1 := Recompute(mustFacts(t, fs, v), bounds(), v)
	if c1.StopStep != 0 {
		t.Fatalf("revised facts should not stop, got step %d", c1.StopStep)
	}
	if CompareConclusions(c0, c1) != ShiftVanished {
		t.Fatalf("want vanished shift, got %s", CompareConclusions(c0, c1))
	}
	// Original facts remain intact at version 0.
	again := Recompute(mustFacts(t, fs, 0), bounds(), 0)
	if again.StopStep != 3 {
		t.Fatalf("revision rewrote history: version-0 stop now %d", again.StopStep)
	}
}

func TestStopMovesEarlier(t *testing.T) {
	fs := NewFactStore(baseFacts())
	c0 := Recompute(mustFacts(t, fs, 0), bounds(), 0)
	v, _ := fs.Revise(Revision{ObsID: "o2", Value: 9}) // sums 3,12 -> stop step 2
	c1 := Recompute(mustFacts(t, fs, v), bounds(), v)
	if c1.StopStep != 2 || CompareConclusions(c0, c1) != ShiftEarlier {
		t.Fatalf("want earlier stop at 2, got %+v", c1)
	}
}

func TestStopMovesLater(t *testing.T) {
	facts := []Observation{
		{ID: "o1", Time: 1, Seq: 1, Value: 3},
		{ID: "o2", Time: 2, Seq: 1, Value: 9}, // v0: stop step 2
		{ID: "o3", Time: 3, Seq: 1, Value: 5},
	}
	fs := NewFactStore(facts)
	c0 := Recompute(mustFacts(t, fs, 0), bounds(), 0)
	v, _ := fs.Revise(Revision{ObsID: "o2", Value: 4}) // sums 3,7,12 -> stop step 3
	c1 := Recompute(mustFacts(t, fs, v), bounds(), v)
	if c1.StopStep != 3 || CompareConclusions(c0, c1) != ShiftLater {
		t.Fatalf("want later stop at 3, got %+v", c1)
	}
}

func TestCompensationNeverTouchesLedger(t *testing.T) {
	fs := NewFactStore(baseFacts())
	c0 := Recompute(mustFacts(t, fs, 0), bounds(), 0)
	l := &Ledger{}
	act := l.Append(Action{Step: 3, Kind: "ship", FactVersion: 0, Note: "rolled out on stop"})

	v, _ := fs.Revise(Revision{ObsID: "o3", Value: 1}) // stop vanishes
	c1 := Recompute(mustFacts(t, fs, v), bounds(), v)
	comps := Compensate(l, c0, c1)
	if len(comps) == 0 {
		t.Fatalf("expected compensation suggestions")
	}
	found := false
	for _, c := range comps {
		if c.ActionID == act.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no compensation targets the executed action: %+v", comps)
	}
	// Ledger untouched: same single entry, same content.
	got := l.Actions()
	if len(got) != 1 || got[0] != act {
		t.Fatalf("ledger mutated: %+v", got)
	}
}

func TestCompensationForEarlierStop(t *testing.T) {
	fs := NewFactStore(baseFacts())
	c0 := Recompute(mustFacts(t, fs, 0), bounds(), 0)
	l := &Ledger{}
	l.Append(Action{Step: 2, Kind: "alert"})
	late := l.Append(Action{Step: 3, Kind: "ship"})
	v, _ := fs.Revise(Revision{ObsID: "o2", Value: 9}) // stop moves to step 2
	c1 := Recompute(mustFacts(t, fs, v), bounds(), v)
	comps := Compensate(l, c0, c1)
	if len(comps) != 1 || comps[0].ActionID != late.ID {
		t.Fatalf("only the post-stop action should be compensated: %+v", comps)
	}
}

func mustFacts(t *testing.T, fs *FactStore, v int) []Observation {
	t.Helper()
	f, err := fs.FactsAt(v)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
