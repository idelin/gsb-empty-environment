package expsim

import "testing"

func stream() []Observation {
	return []Observation{
		{ID: "o1", Time: 1, Seq: 1, Value: 3},
		{ID: "o2", Time: 2, Seq: 1, Value: 4},
		{ID: "o3", Time: 3, Seq: 1, Value: 5}, // sum 12 >= 10: stop
		{ID: "o4", Time: 4, Seq: 1, Value: 7}, // post-stop, ignored
	}
}

func TestCheckpointRestoreContinuesIdentically(t *testing.T) {
	straight := NewEngine(bounds())
	straight.Process(stream()...)

	part := NewEngine(bounds())
	part.Process(stream()[:2]...)
	cp := part.SaveCheckpoint()
	restored := Restore(bounds(), cp)
	restored.Process(stream()[2:]...)

	if restored.Sum() != straight.Sum() || restored.Decision() != straight.Decision() {
		t.Fatalf("restore diverged: sum=%d/%d decision=%s/%s",
			restored.Sum(), straight.Sum(), restored.Decision(), straight.Decision())
	}
	if *restored.Evidence() != *straight.Evidence() {
		t.Fatalf("evidence diverged: %v vs %v", restored.Evidence(), straight.Evidence())
	}
	// Replayed pre-checkpoint events must be dropped as duplicates.
	restored.Process(stream()...)
	n, dups, ignored := restored.Counts()
	sn, sdups, signored := straight.Counts()
	if n != sn || ignored != signored || dups != sdups+4 {
		t.Fatalf("counts after replay: (%d,%d,%d) vs straight (%d,%d,%d)", n, dups, ignored, sn, sdups, signored)
	}
}

func TestCheckpointPreservesStopEvidence(t *testing.T) {
	e := NewEngine(bounds())
	e.Process(stream()...)
	cp := e.SaveCheckpoint()
	if cp.Evidence == nil || cp.Evidence.Step != 3 || cp.Evidence.Sum != 12 {
		t.Fatalf("checkpoint lost stop evidence: %+v", cp.Evidence)
	}
	r := Restore(bounds(), cp)
	if !r.Stopped() || r.Decision() != DecisionUpper || *r.Evidence() != *cp.Evidence {
		t.Fatalf("restored engine lost the frozen conclusion")
	}
}

func TestVerifyCheckpointExactTouchAndRepeats(t *testing.T) {
	raw := append([]Observation{}, stream()...)
	raw = append(raw, stream()[0], stream()[1]) // repeated events
	e := NewEngine(bounds())
	e.Process(raw...)
	cp := e.SaveCheckpoint()
	if err := VerifyCheckpoint(raw, bounds(), cp); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
}

func TestVerifyStopRejectsNonFirstTouch(t *testing.T) {
	// Evidence claims step 4, but step 3 already touches.
	bad := StopEvidence{Step: 4, Sum: 19, Upper: 10, Lower: -10, Decision: DecisionUpper,
		ObsID: "o4", ObsTime: 4, ObsSeq: 1, ObsValue: 7}
	if err := VerifyStop(stream(), bounds(), bad); err == nil {
		t.Fatalf("accepted evidence past the first touch")
	}
}

func TestVerifyStopRejectsTamperedSum(t *testing.T) {
	bad := StopEvidence{Step: 3, Sum: 13, Upper: 10, Lower: -10, Decision: DecisionUpper,
		ObsID: "o3", ObsTime: 3, ObsSeq: 1, ObsValue: 5}
	if err := VerifyStop(stream(), bounds(), bad); err == nil {
		t.Fatalf("accepted tampered sum")
	}
}

func TestVerifyCheckpointDetectsDoubleCounting(t *testing.T) {
	raw := append([]Observation{}, stream()...)
	raw = append(raw, stream()[0]) // one repeat
	cp := Checkpoint{Processed: 3, Sum: 12, Stopped: true, Decision: DecisionUpper,
		Dups: 0, // wrong: log contains one repeated event
		Evidence: &StopEvidence{Step: 3, Sum: 12, Upper: 10, Lower: -10, Decision: DecisionUpper,
			ObsID: "o3", ObsTime: 3, ObsSeq: 1, ObsValue: 5}}
	if err := VerifyCheckpoint(raw, bounds(), cp); err == nil {
		t.Fatalf("accepted checkpoint that ignored a repeated event")
	}
}
