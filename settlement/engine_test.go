package settlement

import "testing"

func mustEngine(t *testing.T, rule Rule) *Engine {
	t.Helper()
	e, err := NewEngine(rule)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustSubmit(t *testing.T, e *Engine, ev ClaimEvent) {
	t.Helper()
	if err := e.Submit(ev); err != nil {
		t.Fatalf("Submit(%s): %v", ev.ID, err)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate after %s: %v", ev.ID, err)
	}
}

func recordsFor(e *Engine, eventID string) []Record {
	var out []Record
	for _, r := range e.Ledger() {
		if r.EventID == eventID {
			out = append(out, r)
		}
	}
	return out
}

func TestWaitingPeriodAndCoverageWindow(t *testing.T) {
	e := mustEngine(t, Rule{
		CoverageStart: "2025-01-01",
		CoverageEnd:   "2025-12-31",
		WaitingDays:   30, // eligible from 2025-01-31
	})

	mustSubmit(t, e, ClaimEvent{ID: "before", IncidentDate: "2024-12-31", Amount: 1000})
	mustSubmit(t, e, ClaimEvent{ID: "waiting", IncidentDate: "2025-01-15", Amount: 1000})
	mustSubmit(t, e, ClaimEvent{ID: "edge", IncidentDate: "2025-01-31", Amount: 1000})
	mustSubmit(t, e, ClaimEvent{ID: "after", IncidentDate: "2026-01-01", Amount: 1000})

	net := e.NetPaid()
	if net["before"] != 0 || net["waiting"] != 0 || net["after"] != 0 {
		t.Fatalf("expected denials, got %v", net)
	}
	if net["edge"] != 1000 {
		t.Fatalf("waiting-period boundary should pay, got %d", net["edge"])
	}

	wantReason := map[string]string{"before": ReasonOutOfCoverage, "waiting": ReasonWaitingPeriod, "after": ReasonOutOfCoverage}
	for id, reason := range wantReason {
		recs := recordsFor(e, id)
		if len(recs) != 1 || recs[0].Type != RecordDenied {
			t.Fatalf("%s: want one DENIED record, got %+v", id, recs)
		}
		if len(recs[0].Deductions) != 1 || recs[0].Deductions[0].Reason != reason || recs[0].Deductions[0].Amount != 1000 {
			t.Fatalf("%s: want full deduction %s, got %+v", id, reason, recs[0].Deductions)
		}
	}
}

func TestPerEventDeductibleAndPerEventLimit(t *testing.T) {
	e := mustEngine(t, Rule{
		CoverageStart: "2025-01-01",
		CoverageEnd:   "2025-12-31",
		Deductible:    &DeductibleRule{Scope: DeductiblePerEvent, Amount: 100},
		Limits:        []LimitRule{{ID: "per", Scope: ScopePerEvent, Amount: 1000}},
	})

	mustSubmit(t, e, ClaimEvent{ID: "small", IncidentDate: "2025-03-01", Amount: 500})
	mustSubmit(t, e, ClaimEvent{ID: "big", IncidentDate: "2025-03-02", Amount: 2000})

	net := e.NetPaid()
	if net["small"] != 400 {
		t.Fatalf("small: want 400 (500-100 deductible), got %d", net["small"])
	}
	if net["big"] != 1000 {
		t.Fatalf("big: want 1000 (2000-100 capped at 1000), got %d", net["big"])
	}

	recs := recordsFor(e, "big")
	if len(recs) != 1 || len(recs[0].Deductions) != 2 {
		t.Fatalf("big: want 2 deductions, got %+v", recs)
	}
	d := recs[0].Deductions
	if d[0].Reason != ReasonDeductible || d[0].Amount != 100 {
		t.Fatalf("big: first deduction %+v", d[0])
	}
	if d[1].Reason != ReasonPerEventLimit || d[1].Amount != 900 || d[1].LimitID != "per" {
		t.Fatalf("big: second deduction %+v", d[1])
	}
}

func TestAggregateDeductibleConsumedInOrder(t *testing.T) {
	e := mustEngine(t, Rule{
		CoverageStart: "2025-01-01",
		CoverageEnd:   "2025-12-31",
		Deductible:    &DeductibleRule{Scope: DeductibleAggregate, Amount: 300},
	})

	mustSubmit(t, e, ClaimEvent{ID: "e1", IncidentDate: "2025-02-01", Amount: 200})
	mustSubmit(t, e, ClaimEvent{ID: "e2", IncidentDate: "2025-03-01", Amount: 200})
	mustSubmit(t, e, ClaimEvent{ID: "e3", IncidentDate: "2025-04-01", Amount: 200})

	net := e.NetPaid()
	if net["e1"] != 0 || net["e2"] != 100 || net["e3"] != 200 {
		t.Fatalf("want 0/100/200, got %v", net)
	}
}

func TestSharedAggregateLimitAcrossCategories(t *testing.T) {
	e := mustEngine(t, Rule{
		CoverageStart: "2025-01-01",
		CoverageEnd:   "2025-12-31",
		Limits: []LimitRule{
			{ID: "shared", Scope: ScopeAggregate, Amount: 1000, Categories: []string{"a", "b"}},
		},
	})

	mustSubmit(t, e, ClaimEvent{ID: "a1", IncidentDate: "2025-02-01", Amount: 600, Category: "a"})
	mustSubmit(t, e, ClaimEvent{ID: "b1", IncidentDate: "2025-03-01", Amount: 600, Category: "b"})

	net := e.NetPaid()
	if net["a1"] != 600 || net["b1"] != 400 {
		t.Fatalf("want 600/400, got %v", net)
	}
	recs := recordsFor(e, "b1")
	if len(recs) != 1 || len(recs[0].Deductions) != 1 {
		t.Fatalf("b1: want 1 deduction, got %+v", recs)
	}
	d := recs[0].Deductions[0]
	if d.Reason != ReasonSharedLimit || d.Amount != 200 || d.LimitID != "shared" {
		t.Fatalf("b1: deduction %+v", d)
	}
	if got := e.Summarize().TotalPaid; got != 1000 {
		t.Fatalf("total paid %d exceeds shared limit 1000", got)
	}
}

func TestLateEventTriggersClawback(t *testing.T) {
	e := mustEngine(t, Rule{
		CoverageStart: "2025-01-01",
		CoverageEnd:   "2025-12-31",
		Limits: []LimitRule{
			{ID: "shared", Scope: ScopeAggregate, Amount: 1000},
		},
	})

	mustSubmit(t, e, ClaimEvent{ID: "e1", IncidentDate: "2025-03-01", Amount: 600})
	mustSubmit(t, e, ClaimEvent{ID: "e2", IncidentDate: "2025-04-01", Amount: 600})
	if net := e.NetPaid(); net["e1"] != 600 || net["e2"] != 400 {
		t.Fatalf("before late event: want 600/400, got %v", net)
	}

	// Late arrival with the earliest incident date takes priority.
	mustSubmit(t, e, ClaimEvent{ID: "late", IncidentDate: "2025-02-01", Amount: 500})

	net := e.NetPaid()
	if net["late"] != 500 || net["e1"] != 500 || net["e2"] != 0 {
		t.Fatalf("after late event: want 500/500/0, got %v", net)
	}

	var clawE1, clawE2 *Record
	for i, r := range e.Ledger() {
		if r.Type == RecordClawback && r.EventID == "e1" {
			clawE1 = &e.Ledger()[i]
		}
		if r.Type == RecordClawback && r.EventID == "e2" {
			clawE2 = &e.Ledger()[i]
		}
	}
	if clawE1 == nil || clawE1.Amount != -100 || clawE1.Trigger != "late" {
		t.Fatalf("e1 clawback: %+v", clawE1)
	}
	if clawE2 == nil || clawE2.Amount != -400 || clawE2.Trigger != "late" {
		t.Fatalf("e2 clawback: %+v", clawE2)
	}
	if got := e.Summarize().TotalPaid; got != 1000 {
		t.Fatalf("total paid %d exceeds shared limit 1000", got)
	}
}

func TestLateEventTriggersCatchUp(t *testing.T) {
	e := mustEngine(t, Rule{
		CoverageStart: "2025-01-01",
		CoverageEnd:   "2025-12-31",
		Deductible:    &DeductibleRule{Scope: DeductibleAggregate, Amount: 300},
	})

	mustSubmit(t, e, ClaimEvent{ID: "e1", IncidentDate: "2025-02-01", Amount: 200})
	mustSubmit(t, e, ClaimEvent{ID: "e2", IncidentDate: "2025-03-01", Amount: 200})
	if net := e.NetPaid(); net["e1"] != 0 || net["e2"] != 100 {
		t.Fatalf("before late event: want 0/100, got %v", net)
	}

	// The late event absorbs part of the deductible, freeing payout for e2.
	mustSubmit(t, e, ClaimEvent{ID: "late", IncidentDate: "2025-01-15", Amount: 100})

	net := e.NetPaid()
	if net["late"] != 0 || net["e1"] != 0 || net["e2"] != 200 {
		t.Fatalf("after late event: want 0/0/200, got %v", net)
	}
	var catchUp *Record
	for i, r := range e.Ledger() {
		if r.Type == RecordCatchUp && r.EventID == "e2" {
			catchUp = &e.Ledger()[i]
		}
	}
	if catchUp == nil || catchUp.Amount != 100 || catchUp.Trigger != "late" {
		t.Fatalf("e2 catch-up: %+v", catchUp)
	}
}

func TestSameDateEventsOrderedByArrival(t *testing.T) {
	e := mustEngine(t, Rule{
		CoverageStart: "2025-01-01",
		CoverageEnd:   "2025-12-31",
		Limits: []LimitRule{
			{ID: "shared", Scope: ScopeAggregate, Amount: 100},
		},
	})

	mustSubmit(t, e, ClaimEvent{ID: "first", IncidentDate: "2025-05-01", Amount: 80})
	mustSubmit(t, e, ClaimEvent{ID: "second", IncidentDate: "2025-05-01", Amount: 80})

	net := e.NetPaid()
	if net["first"] != 80 || net["second"] != 20 {
		t.Fatalf("arrival order tie-break: want 80/20, got %v", net)
	}
}

func TestLimitNeverExceededUnderLateArrivals(t *testing.T) {
	e := mustEngine(t, Rule{
		CoverageStart: "2025-01-01",
		CoverageEnd:   "2025-12-31",
		Deductible:    &DeductibleRule{Scope: DeductibleAggregate, Amount: 250},
		Limits: []LimitRule{
			{ID: "cat-a", Scope: ScopeAggregate, Amount: 700, Categories: []string{"a"}},
			{ID: "global", Scope: ScopeAggregate, Amount: 1500},
		},
	})

	// Deterministic pseudo-random arrival order with shuffled dates.
	events := []ClaimEvent{
		{ID: "e1", IncidentDate: "2025-06-10", Amount: 400, Category: "a"},
		{ID: "e2", IncidentDate: "2025-01-20", Amount: 300, Category: "b"},
		{ID: "e3", IncidentDate: "2025-03-15", Amount: 500, Category: "a"},
		{ID: "e4", IncidentDate: "2025-02-01", Amount: 200, Category: "b"},
		{ID: "e5", IncidentDate: "2025-08-01", Amount: 900, Category: "a"},
		{ID: "e6", IncidentDate: "2025-04-05", Amount: 150, Category: "b"},
		{ID: "e7", IncidentDate: "2025-09-01", Amount: 600, Category: "b"},
	}
	for _, ev := range events {
		mustSubmit(t, e, ev) // Validate runs after every submit
	}

	s := e.Summarize()
	for _, u := range s.LimitUsage {
		if u.Used > u.Cap {
			t.Fatalf("limit %s exceeded: %d > %d", u.LimitID, u.Used, u.Cap)
		}
	}
	if s.TotalPaid != 1500 {
		t.Fatalf("global limit 1500 should be exactly exhausted, got %d", s.TotalPaid)
	}
	var catA int64
	for _, u := range s.LimitUsage {
		if u.LimitID == "cat-a" {
			catA = u.Used
		}
	}
	if catA != 700 {
		t.Fatalf("cat-a limit 700 should be exactly exhausted, got %d", catA)
	}
}

func TestRejectsInvalidInput(t *testing.T) {
	if _, err := NewEngine(Rule{CoverageStart: "2025-12-31", CoverageEnd: "2025-01-01"}); err == nil {
		t.Fatal("want error for inverted coverage window")
	}
	if _, err := NewEngine(Rule{
		CoverageStart: "2025-01-01", CoverageEnd: "2025-12-31",
		Limits: []LimitRule{{ID: "x", Scope: ScopeAggregate, Amount: 1}, {ID: "x", Scope: ScopePerEvent, Amount: 1}},
	}); err == nil {
		t.Fatal("want error for duplicate limit id")
	}

	e := mustEngine(t, Rule{CoverageStart: "2025-01-01", CoverageEnd: "2025-12-31"})
	if err := e.Submit(ClaimEvent{ID: "bad", IncidentDate: "2025-13-01", Amount: 1}); err == nil {
		t.Fatal("want error for invalid date")
	}
	mustSubmit(t, e, ClaimEvent{ID: "ok", IncidentDate: "2025-01-01", Amount: 1})
	if err := e.Submit(ClaimEvent{ID: "ok", IncidentDate: "2025-01-02", Amount: 1}); err == nil {
		t.Fatal("want error for duplicate event id")
	}
}
