package cep

import (
	"strings"
	"testing"
)

// Acceptance boundary: A@0, B@5 -> hit; then late C@2 arrives ->
// the previous hit is precisely retracted and logged.
func TestAcceptance_HitThenLateCRetracts(t *testing.T) {
	eng := NewEngine(10)

	eng.Process(Event{Type: "A", Time: 0})
	eng.Process(Event{Type: "B", Time: 5})

	notes := eng.Notifications()
	if len(notes) != 1 || notes[0].Kind != NotifyMatch {
		t.Fatalf("expected 1 MATCH, got %+v", notes)
	}
	if notes[0].Match.A.Time != 0 || notes[0].Match.B.Time != 5 {
		t.Fatalf("unexpected match content: %+v", notes[0].Match)
	}

	// Late out-of-order event: C happened at t=2, inside (A, B).
	eng.Process(Event{Type: "C", Time: 2})

	notes = eng.Notifications()
	if len(notes) != 2 {
		t.Fatalf("expected MATCH + RETRACT, got %+v", notes)
	}
	retract := notes[1]
	if retract.Kind != NotifyRetract {
		t.Fatalf("expected RETRACT, got %v", retract.Kind)
	}
	if retract.Match.A.Time != 0 || retract.Match.B.Time != 5 {
		t.Fatalf("retraction must reference the original match, got %+v", retract.Match)
	}
	if retract.CausedBy.Type != "C" || retract.CausedBy.Time != 2 {
		t.Fatalf("retraction must be caused by late C@2, got %+v", retract.CausedBy)
	}

	found := false
	for _, line := range eng.Log() {
		if strings.Contains(line, "RETRACT") && strings.Contains(line, "C@2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("retraction not recorded in log: %v", eng.Log())
	}
}

// B beyond the 10s window must not produce a hit.
func TestWindowBoundary(t *testing.T) {
	eng := NewEngine(10)
	eng.Process(Event{Type: "A", Time: 0})
	eng.Process(Event{Type: "B", Time: 10}) // exactly at boundary: allowed
	if n := eng.Notifications(); len(n) != 1 || n[0].Kind != NotifyMatch {
		t.Fatalf("B@10 within inclusive window should match, got %+v", n)
	}

	eng2 := NewEngine(10)
	eng2.Process(Event{Type: "A", Time: 0})
	eng2.Process(Event{Type: "B", Time: 11}) // outside window
	if n := eng2.Notifications(); len(n) != 0 {
		t.Fatalf("B@11 outside window must not match, got %+v", n)
	}
}

// C between A and B (in-order) suppresses the match entirely.
func TestNegationInOrder(t *testing.T) {
	eng := NewEngine(10)
	eng.Process(Event{Type: "A", Time: 0})
	eng.Process(Event{Type: "C", Time: 3})
	eng.Process(Event{Type: "B", Time: 5})
	if n := eng.Notifications(); len(n) != 0 {
		t.Fatalf("C between A and B must suppress match, got %+v", n)
	}
}

// C after B does not affect an already completed match.
func TestCAfterMatchIsHarmless(t *testing.T) {
	eng := NewEngine(10)
	eng.Process(Event{Type: "A", Time: 0})
	eng.Process(Event{Type: "B", Time: 5})
	eng.Process(Event{Type: "C", Time: 8})
	notes := eng.Notifications()
	if len(notes) != 1 || notes[0].Kind != NotifyMatch {
		t.Fatalf("C after B must not retract, got %+v", notes)
	}
}

// A pending run is discarded once the window expires (timeout).
func TestTimeoutDiscard(t *testing.T) {
	eng := NewEngine(10)
	eng.Process(Event{Type: "A", Time: 0})
	eng.Process(Event{Type: "A", Time: 20}) // advances clock, expires first run
	eng.Process(Event{Type: "B", Time: 25}) // matches only the second A

	notes := eng.Notifications()
	if len(notes) != 1 || notes[0].Match.A.Time != 20 {
		t.Fatalf("expected match on A@20 only, got %+v", notes)
	}
	found := false
	for _, line := range eng.Log() {
		if strings.Contains(line, "discarded") && strings.Contains(line, "A@0") {
			found = true
		}
	}
	if !found {
		t.Fatalf("timeout discard not logged: %v", eng.Log())
	}
}

// Out-of-order B arriving after its event-time still matches correctly.
func TestOutOfOrderB(t *testing.T) {
	eng := NewEngine(10)
	eng.Process(Event{Type: "A", Time: 0})
	eng.Process(Event{Type: "A", Time: 8})
	eng.Process(Event{Type: "B", Time: 4}) // late B for the first A
	notes := eng.Notifications()
	if len(notes) != 1 || notes[0].Match.A.Time != 0 || notes[0].Match.B.Time != 4 {
		t.Fatalf("late B@4 should match A@0 only, got %+v", notes)
	}
}
