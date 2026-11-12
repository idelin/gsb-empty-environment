package lsadb

import (
	"errors"
	"testing"
	"time"
)

func errorIs(err, target error) bool { return errors.Is(err, target) }

func TestAuthTrackerMonotonic(t *testing.T) {
	t.Parallel()

	tracker := NewAuthTracker()

	if !tracker.Check(neighborID, 100) {
		t.Fatal("first authentication sequence must be accepted")
	}
	if !tracker.Check(neighborID, 100) {
		t.Fatal("duplicate authentication sequence must be accepted")
	}
	if got, ok := tracker.Last(neighborID); !ok || got != 100 {
		t.Fatalf("last sequence = (%d, %v), want (100, true)", got, ok)
	}
	if !tracker.Check(neighborID, 101) {
		t.Fatal("advancing authentication sequence must be accepted")
	}
	if tracker.Check(neighborID, 100) {
		t.Fatal("regressing authentication sequence must be rejected")
	}

	// Neighbors are tracked independently.
	if !tracker.Check(attackerID, 1) {
		t.Fatal("first sequence for a different neighbor must be accepted")
	}

	tracker.Reset()
	if _, ok := tracker.Last(neighborID); ok {
		t.Fatal("Reset did not forget neighbor state")
	}
	if !tracker.Check(neighborID, 5) {
		t.Fatal("sequence after Reset must be accepted")
	}
}

func TestAdmitRejectsAuthRegression(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, time.Second, clock).WithAuth(NewAuthTracker())
	lsa := buildLSA(t, neighborID, 1, testLSID, InitSequenceNumber, 10, nil)

	if d := db.Admit(neighborID, 50, lsa); d.Outcome != OutcomeAccept {
		t.Fatalf("first packet outcome = %s, want accept", d.Outcome)
	}

	replay := buildLSA(t, neighborID, 1, testLSID, InitSequenceNumber+1, 10, nil)
	d := db.Admit(neighborID, 49, replay)
	if d.Outcome != OutcomeAuthRejected || !errorIs(d.Err, ErrAuthSequence) {
		t.Fatalf("replay outcome = %s err = %v, want auth-rejected", d.Outcome, d.Err)
	}
	if db.Len() != 1 {
		t.Fatalf("database length = %d, replayed packet must not change it", db.Len())
	}
	if got := db.Stats().AuthRejected.Load(); got != 1 {
		t.Fatalf("auth_rejected = %d, want 1", got)
	}
}
