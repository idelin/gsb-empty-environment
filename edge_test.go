package netsec

import "testing"

func TestCheckEmptyHistory(t *testing.T) {
	report, err := Check(History{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable || len(report.Order) != 0 {
		t.Fatalf("empty history must linearize with empty order: %+v", report)
	}
}

func TestCheckInstantIntervals(t *testing.T) {
	// Zero-length intervals with distinct points are strictly ordered.
	h := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 1, Return: 1, Result: OK},
		op(2, Grant, "c1", 2, 2, OK),
		op(3, Grant, "c1", 3, 3, ErrConsumed),
	}}
	report, err := Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("ordered instant history must linearize: %+v", report.Counterexample)
	}
}

func TestCheckLockedAfterBudgetBlocksGrant(t *testing.T) {
	h := History{WrongLimit: 1, Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		{ID: 2, Kind: Answer, ChallengeID: "c1", Answer: "x", Invoke: 2, Return: 3, Result: ErrLocked},
		op(4, Grant, "c1", 4, 5, OK),
	}}
	report, err := Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linearizable {
		t.Fatal("grant after lock cannot succeed")
	}

	h.Operations[2].Result = ErrLocked
	report, err = Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("grant observing locked should linearize: %+v", report.Counterexample)
	}
}

func TestCheckConsumeAndGrantRace(t *testing.T) {
	// Consume and Grant overlap: exactly one succeeds; the other sees
	// ErrConsumed. Both observing OK is impossible.
	bad := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		op(2, Consume, "c1", 2, 6, OK),
		op(3, Grant, "c1", 3, 5, OK),
	}}
	report, err := Check(bad)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linearizable {
		t.Fatal("consume and grant cannot both succeed")
	}

	good := bad
	good.Operations = append([]Operation(nil), bad.Operations...)
	good.Operations[2].Result = ErrConsumed
	report, err = Check(good)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("consume-first ordering must exist: %+v", report.Counterexample)
	}
}

func TestCheckCreateReplay(t *testing.T) {
	h := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		{ID: 2, Kind: Create, ChallengeID: "c1", Answer: "b", Invoke: 2, Return: 3, Result: ErrReplay},
	}}
	report, err := Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("replay observation must linearize: %+v", report.Counterexample)
	}
}

func TestStringers(t *testing.T) {
	if Grant.String() != "grant" {
		t.Fatalf("got %q", Grant.String())
	}
	if ErrExpired.String() != "expired" {
		t.Fatalf("got %q", ErrExpired.String())
	}
	if Kind(99).String() != "unknown" || Result(99).String() != "unknown" {
		t.Fatal("unknown values must be labeled")
	}
}
