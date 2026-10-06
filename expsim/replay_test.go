package expsim

import "testing"

func scenarioEvents() []Event {
	return []Event{
		ObserveEvent(Observation{ID: "o1", Time: 1, Seq: 1, Value: 3}),
		ObserveEvent(Observation{ID: "o2", Time: 2, Seq: 1, Value: 4}),
		CheckpointEvent(),
		ObserveEvent(Observation{ID: "o3", Time: 3, Seq: 1, Value: 5}), // v0 stop step 3
		ActionEvent(Action{Step: 3, Kind: "ship"}),
		ObserveEvent(Observation{ID: "o4", Time: 4, Seq: 1, Value: 7}), // ignored, frozen
		CheckpointEvent(),
		ReviseEvent(Revision{ObsID: "o2", Value: 9, Note: "sensor recalibrated"}),    // stop moves to step 2
		ReviseEvent(Revision{ObsID: "o2", Value: 4, Note: "recalibration reverted"}), // stop back to step 3
	}
}

func TestReplayRecovery(t *testing.T) {
	if err := VerifyRecovery(bounds(), scenarioEvents()); err != nil {
		t.Fatalf("recovery verification failed: %v", err)
	}
}

func TestReplayTracksVersionsAndShifts(t *testing.T) {
	s, err := Replay(bounds(), scenarioEvents())
	if err != nil {
		t.Fatal(err)
	}
	cs := s.Conclusions()
	if len(cs) != 2 {
		t.Fatalf("want 2 recomputed conclusions, got %d", len(cs))
	}
	if cs[0].FactVersion != 1 || cs[0].StopStep != 2 {
		t.Fatalf("v1 conclusion: %+v", cs[0])
	}
	if cs[1].FactVersion != 2 || cs[1].StopStep != 3 {
		t.Fatalf("v2 conclusion: %+v", cs[1])
	}
	shifts := s.Shifts()
	if len(shifts) != 2 || shifts[0] != ShiftEarlier || shifts[1] != ShiftLater {
		t.Fatalf("shifts: %v", shifts)
	}
	// Live engine conclusion stays frozen at the version-0 stop.
	if s.Engine().Evidence().Step != 3 || s.Engine().Decision() != DecisionUpper {
		t.Fatalf("live engine conclusion changed: %v", s.Engine().Evidence())
	}
}

func TestReplayCompensationsForMovedStop(t *testing.T) {
	s, err := Replay(bounds(), scenarioEvents())
	if err != nil {
		t.Fatal(err)
	}
	comps := s.Compensations()
	if len(comps) == 0 {
		t.Fatalf("expected compensation suggestions after stop moved")
	}
	// The ship action at step 3 must be compensated under v1 (stop at 2)...
	targeted := false
	for _, c := range comps {
		if c.ActionID == "act-1" && c.ActionStep == 3 {
			targeted = true
		}
	}
	if !targeted {
		t.Fatalf("ship action never compensated: %+v", comps)
	}
	// ...while the ledger itself keeps exactly one untouched entry.
	acts := s.Ledger().Actions()
	if len(acts) != 1 || acts[0].Kind != "ship" || acts[0].FactVersion != 0 {
		t.Fatalf("ledger mutated: %+v", acts)
	}
	// Fact versions are all retained.
	if s.Facts().Version() != 2 {
		t.Fatalf("fact versions lost: %d", s.Facts().Version())
	}
}

func TestRecoveryDetectsTamperedCheckpoint(t *testing.T) {
	events := scenarioEvents()
	s, err := Replay(bounds(), events)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the second checkpoint's evidence and require the
	// prefix-exhaustive check to reject it.
	s.checkpoints[1].Evidence.Sum = 999
	raw := []Observation{
		{ID: "o1", Time: 1, Seq: 1, Value: 3},
		{ID: "o2", Time: 2, Seq: 1, Value: 4},
		{ID: "o3", Time: 3, Seq: 1, Value: 5},
		{ID: "o4", Time: 4, Seq: 1, Value: 7},
	}
	if err := VerifyCheckpoint(raw, bounds(), s.checkpoints[1]); err == nil {
		t.Fatalf("tampered checkpoint accepted")
	}
}
