package netsec

import "testing"

func op(id int, kind Kind, challenge string, invoke, ret int64, result Result) Operation {
	return Operation{ID: id, Kind: kind, ChallengeID: challenge, Invoke: invoke, Return: ret, Result: result}
}

func TestCheckSequentialHistory(t *testing.T) {
	h := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		{ID: 2, Kind: Answer, ChallengeID: "c1", Answer: "a", Invoke: 2, Return: 3, Result: OK},
		{ID: 3, Kind: Grant, ChallengeID: "c1", Invoke: 4, Return: 5, Result: OK},
		{ID: 4, Kind: Grant, ChallengeID: "c1", Invoke: 6, Return: 7, Result: ErrConsumed},
	}}
	report, err := Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("sequential history must linearize: %+v", report.Counterexample)
	}
	if err := VerifyOrder(h, report.Order); err != nil {
		t.Fatalf("returned order invalid: %v", err)
	}
}

func TestCheckStaleGrantAfterObservedReplacementRejected(t *testing.T) {
	// The replacement has already returned before the stale grant invokes,
	// so the grant on retired c1 cannot succeed in any legal ordering.
	h := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "b", Invoke: 2, Return: 3, Result: OK},
		op(4, Grant, "c1", 4, 5, OK),
	}}
	report, err := Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linearizable {
		t.Fatal("stale grant success after an observed replacement must be a counterexample")
	}
	if report.Counterexample == nil || len(report.Counterexample.OperationIDs) == 0 {
		t.Fatal("counterexample must carry the minimal operation set")
	}
}

func TestCheckConcurrentStaleGrantHasLegalOrder(t *testing.T) {
	// When replace and the stale grant truly overlap, an OK grant is
	// explainable by linearizing the grant first.
	h := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "b", Invoke: 2, Return: 5, Result: OK},
		op(3, Grant, "c1", 3, 4, OK),
	}}
	report, err := Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("overlapping grant before replacement must linearize: %+v", report.Counterexample)
	}
	if err := VerifyOrder(h, report.Order); err != nil {
		t.Fatalf("order invalid: %v", err)
	}
}

func TestCheckStaleGrantRaceExpiredIsLinearizable(t *testing.T) {
	// Same race, but the stale grant correctly observes ErrExpired: the
	// replacement linearizes first, so this history is linearizable.
	h := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "b", Invoke: 2, Return: 5, Result: OK},
		op(3, Grant, "c1", 3, 4, ErrExpired),
		op(4, Grant, "c2", 6, 7, OK),
	}}
	report, err := Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("correctly expired grant should linearize: %+v", report.Counterexample)
	}
	if err := VerifyOrder(h, report.Order); err != nil {
		t.Fatalf("order invalid: %v", err)
	}
}

func TestCheckDoubleGrantOnlyOneSucceeds(t *testing.T) {
	// Two concurrent grants on the only current challenge: exactly one may
	// observe OK. Two OKs cannot be linearized.
	twoOK := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		op(2, Grant, "c1", 2, 5, OK),
		op(3, Grant, "c1", 2, 5, OK),
	}}
	report, err := Check(twoOK)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linearizable {
		t.Fatal("two successful grants cannot linearize")
	}

	oneOK := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		op(2, Grant, "c1", 2, 5, OK),
		op(3, Grant, "c1", 2, 5, ErrConsumed),
	}}
	report, err = Check(oneOK)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("one OK plus one consumed must linearize: %+v", report.Counterexample)
	}
	if err := VerifyOrder(oneOK, report.Order); err != nil {
		t.Fatalf("order invalid: %v", err)
	}
}

func TestCheckWrongAnswerLimitConcurrent(t *testing.T) {
	// WrongLimit=2, three concurrent wrong answers. Only two can execute
	// before the lock, so one observed ErrWrongAnswer among ErrLocked results
	// is required; three ErrWrongAnswer outcomes are impossible.
	makeHistory := func(results ...Result) History {
		ops := []Operation{
			{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "secret", Invoke: 0, Return: 1, Result: OK},
		}
		for i, result := range results {
			ops = append(ops, Operation{
				ID:          i + 2,
				Kind:        Answer,
				ChallengeID: "c1",
				Answer:      "bad",
				Invoke:      2,
				Return:      9,
				Result:      result,
			})
		}
		return History{Operations: ops, WrongLimit: 2}
	}

	good := makeHistory(ErrWrongAnswer, ErrLocked, ErrLocked)
	report, err := Check(good)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("budget-respecting history should linearize: %+v", report.Counterexample)
	}

	bad := makeHistory(ErrWrongAnswer, ErrWrongAnswer, ErrLocked)
	report, err = Check(bad)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linearizable {
		t.Fatal("three wrong answers with limit two cannot linearize")
	}
}

func TestCheckReplacementRetiresOldAnswer(t *testing.T) {
	// Concurrent replace and correct answer on the old challenge. An OK
	// answer is legal when it linearizes first; an ErrExpired answer is not
	// a defined answer result — answer on a non-current challenge still
	// checks the code, so wrong code yields ErrWrongAnswer/locked regardless.
	h := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "b", Invoke: 2, Return: 6, Result: OK},
		{ID: 3, Kind: Answer, ChallengeID: "c1", Answer: "a", Invoke: 3, Return: 5, Result: OK},
		{ID: 4, Kind: Grant, ChallengeID: "c2", Invoke: 7, Return: 8, Result: OK},
	}}
	report, err := Check(h)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Linearizable {
		t.Fatalf("answer-before-replace ordering should linearize: %+v", report.Counterexample)
	}
}

func TestVerifyOrderRejectsBadPermutation(t *testing.T) {
	h := History{Operations: []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: OK},
		op(2, Grant, "c1", 2, 3, OK),
	}}
	if err := VerifyOrder(h, []int{2, 1}); err == nil {
		t.Fatal("grant before create must be rejected")
	}
	if err := VerifyOrder(h, []int{1, 1}); err == nil {
		t.Fatal("duplicate ids must be rejected")
	}
}

func TestValidate(t *testing.T) {
	cases := []History{
		{Operations: []Operation{{ID: 1, Kind: Kind(99), Invoke: 0, Return: 1}}},
		{Operations: []Operation{{ID: 1, Kind: Create, Invoke: 5, Return: 1}}},
		{Operations: []Operation{{ID: 1, Kind: Create, Invoke: 0, Return: 1}}},
		{Operations: []Operation{
			{ID: 1, Kind: Replace, ChallengeID: "c1", Invoke: 0, Return: 1},
		}},
	}
	for i, h := range cases {
		if err := h.Validate(); err == nil {
			t.Fatalf("case %d expected validation error", i)
		}
	}
}
