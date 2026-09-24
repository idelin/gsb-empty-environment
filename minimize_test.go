package cacheaudit

import (
	"testing"
)

func TestMinimizeShrinksNoiseAndKeepsEvidence(t *testing.T) {
	events := []Event{
		// noise on other keys
		pub(1, "x", 1, 1),
		call(2, "x", 1, 2, 3, OutcomeHit),
		{Seq: 3, Kind: KindEvict, Key: "x", At: at(4)},
		pub(4, "y", 1, 1),
		call(5, "y", 1, 2, 3, OutcomeMiss),
		// stale core on key a
		pub(6, "a", 1, 1),
		pub(7, "a", 2, 2),
		call(8, "a", 1, 3, 4, OutcomeHit),
		// extra consistent read on a
		call(9, "a", 2, 5, 6, OutcomeHit),
	}
	rep := Audit(History{Events: events})
	if rep.Verdict != Stale {
		t.Fatalf("audit = %v, want STALE", rep.Verdict)
	}
	min := rep.Minimal
	if len(min) == 0 || len(min) >= len(events) {
		t.Fatalf("minimal subset = %d events (full=%d)", len(min), len(events))
	}
	// Still violates the same contract, with source evidence intact.
	if got := auditCore(History{Events: min}).Verdict; got != Stale {
		t.Fatalf("minimal subset verdict = %v, want STALE", got)
	}
	if !sourcesTraceable(min) {
		t.Fatalf("minimal subset lost source-version evidence: %v", min)
	}
	// The stale call plus both publishes (returned source + newer version)
	// must survive minimization.
	var haveStaleCall, haveV1, haveV2 bool
	for _, e := range min {
		if e.Kind == KindCall && e.Key == "a" && e.Version == 1 {
			haveStaleCall = true
		}
		if e.Kind == KindPublish && e.Key == "a" && e.Version == 1 {
			haveV1 = true
		}
		if e.Kind == KindPublish && e.Key == "a" && e.Version == 2 {
			haveV2 = true
		}
	}
	if !haveStaleCall || !haveV1 || !haveV2 {
		t.Fatalf("minimal subset missing evidence (call=%v v1=%v v2=%v): %v",
			haveStaleCall, haveV1, haveV2, min)
	}
	// 1-minimality: removing any single event must break the violation
	// (or its evidence).
	pred := func(evs []Event) bool {
		return auditCore(History{Events: evs}).Verdict == Stale && sourcesTraceable(evs)
	}
	for i := range min {
		cand := append([]Event{}, min[:i]...)
		cand = append(cand, min[i+1:]...)
		if pred(cand) {
			t.Fatalf("subset is not 1-minimal: removing %v keeps violation", min[i])
		}
	}
}

func TestMinimizeReturnsNilWithoutViolation(t *testing.T) {
	events := []Event{
		pub(1, "a", 1, 1),
		call(2, "a", 1, 2, 3, OutcomeHit),
	}
	if got := Audit(History{Events: events}); got.Minimal != nil {
		t.Fatalf("consistent history produced a minimal subset: %v", got.Minimal)
	}
}
