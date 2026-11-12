package netsec

import "testing"

func TestEnumerateShortHistories(t *testing.T) {
	histories, stats, err := EnumerateShortHistories(EnumerateConfig{
		MaxOps:      6,
		WrongLimit:  2,
		IncludeBugs: true,
	})
	if err != nil {
		t.Fatalf("enumeration reported inconsistency: %v", err)
	}
	if stats.Histories == 0 || len(histories) != stats.Histories {
		t.Fatalf("bad stats: %+v", stats)
	}
	if stats.BugsFound == 0 {
		t.Fatal("expected injected replacement/stale-answer bugs to be rejected")
	}
	if stats.Linearizable+stats.Rejected != stats.Histories {
		t.Fatalf("stats do not partition: %+v", stats)
	}

	// Every clean template must linearize and every returned order must
	// independently verify against the intervals and the specification.
	for _, item := range histories {
		if !item.ExpectLinear {
			continue
		}
		report, err := Check(item.History)
		if err != nil {
			t.Fatal(err)
		}
		if !report.Linearizable {
			t.Fatalf("clean history rejected: %+v", item.History)
		}
		if err := VerifyOrder(item.History, report.Order); err != nil {
			t.Fatalf("returned linearization is invalid: %v", err)
		}
	}
}

func TestEnumerateReplacementStaleAnswerRace(t *testing.T) {
	// Direct scenario: replacement overlaps a stale correct answer. The
	// answer observing OK is explainable only when it linearizes first; the
	// checker must find that order, while flipping the answer to an
	// impossible ErrExpired result must be rejected.
	clean := History{WrongLimit: 2, Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "b", Invoke: 2, Return: 6, Result: OK},
		{ID: 3, Kind: Answer, ChallengeID: "c1", Answer: "a", Invoke: 3, Return: 5, Result: OK},
	}}
	report, err := Check(clean)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("stale answer before replacement should linearize: %+v", report.Counterexample)
	}

	buggy := clean
	buggy.Operations = append([]Operation(nil), clean.Operations...)
	buggy.Operations[2].Result = ErrExpired
	report, err = Check(buggy)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linearizable {
		t.Fatal("answer never produces ErrExpired; mutated history must be rejected")
	}
	if len(report.Counterexample.OperationIDs) == 0 {
		t.Fatal("counterexample must identify operations")
	}
}

func TestEnumerateStatsDefaultConfig(t *testing.T) {
	_, stats, err := EnumerateShortHistories(EnumerateConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Histories == 0 {
		t.Fatal("default config must still generate histories")
	}
}
