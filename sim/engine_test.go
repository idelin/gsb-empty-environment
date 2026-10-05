package sim

import "testing"

func mustDate(t *testing.T, s string) Date {
	t.Helper()
	d, err := ParseDate(s)
	if err != nil {
		t.Fatalf("parse date %s: %v", s, err)
	}
	return d
}

// d 解析日期并在失败时 panic，仅供测试构造字面量使用。
func d(s string) Date {
	date, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return date
}

func ev(id, cov, date string, seq int, amount int64) Event {
	d, _ := ParseDate(date)
	return Event{ID: id, Coverage: cov, ServiceDate: d, Seq: seq, AmountCents: amount}
}

func baseCoverage() Coverage {
	return Coverage{
		ID:                  "c1",
		Window:              Window{Start: d("2025-01-01"), End: d("2025-12-31")},
		WaitingDays:         30,
		DeductibleCents:     1000,
		PerEventLimitCents:  5000,
		AggregateLimitCents: 10000,
	}
}

func newEngine(t *testing.T, p Policy) *Engine {
	t.Helper()
	e, err := NewEngine(p)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func payable(t *testing.T, e *Engine, eventID string) int64 {
	t.Helper()
	for _, s := range e.Report().Settlements {
		if s.EventID == eventID {
			return s.PayableCents
		}
	}
	t.Fatalf("no settlement for event %s", eventID)
	return 0
}

func reasons(e *Engine, eventID string) []Reason {
	for _, s := range e.Report().Settlements {
		if s.EventID == eventID {
			return s.Reasons
		}
	}
	return nil
}

func hasReason(e *Engine, eventID, code string) bool {
	for _, r := range reasons(e, eventID) {
		if r.Code == code {
			return true
		}
	}
	return false
}

func TestOutOfWindowRejected(t *testing.T) {
	c := baseCoverage()
	e := newEngine(t, Policy{Coverages: []Coverage{c}})
	if err := e.Submit(ev("a", "c1", "2026-01-01", 1, 500)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "a"); got != 0 {
		t.Fatalf("payable = %d, want 0", got)
	}
	if !hasReason(e, "a", ReasonOutOfWindow) {
		t.Fatalf("missing OUT_OF_WINDOW reason: %+v", reasons(e, "a"))
	}
	if v := e.Report().Violations; len(v) != 0 {
		t.Fatalf("violations: %v", v)
	}
}

func TestWaitingPeriodRejected(t *testing.T) {
	c := baseCoverage()
	e := newEngine(t, Policy{Coverages: []Coverage{c}})
	// 等待期 30 天：2025-01-15 在等待期内，2025-02-01 不在。
	if err := e.Submit(ev("w", "c1", "2025-01-15", 1, 500)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "w"); got != 0 {
		t.Fatalf("payable = %d, want 0", got)
	}
	if !hasReason(e, "w", ReasonWaitingPeriod) {
		t.Fatalf("missing IN_WAITING_PERIOD reason")
	}
}

func TestDeductiblePerEventAndAggregate(t *testing.T) {
	c := baseCoverage() // 免赔 1000，单次限额 5000，累计限额 10000
	e := newEngine(t, Policy{Coverages: []Coverage{c}})
	// e1: 6000 -> 单次限额截到 5000 -> 免赔 1000 -> 付 4000
	if err := e.Submit(ev("e1", "c1", "2025-03-01", 1, 6000)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "e1"); got != 4000 {
		t.Fatalf("e1 payable = %d, want 4000", got)
	}
	for _, code := range []string{ReasonPerEventLimit, ReasonDeductible} {
		if !hasReason(e, "e1", code) {
			t.Fatalf("e1 missing reason %s: %+v", code, reasons(e, "e1"))
		}
	}
	// e2: 5000 -> 免赔已耗尽 -> 付 5000（累计用至 9000）
	if err := e.Submit(ev("e2", "c1", "2025-04-01", 2, 5000)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "e2"); got != 5000 {
		t.Fatalf("e2 payable = %d, want 5000", got)
	}
	// e3: 5000 -> 累计限额只剩 1000 -> 付 1000
	if err := e.Submit(ev("e3", "c1", "2025-05-01", 3, 5000)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "e3"); got != 1000 {
		t.Fatalf("e3 payable = %d, want 1000", got)
	}
	if !hasReason(e, "e3", ReasonAggregate) {
		t.Fatalf("e3 missing AGGREGATE_LIMIT reason")
	}
	rep := e.Report()
	if rep.TotalsByCoverage["c1"] != 10000 {
		t.Fatalf("total = %d, want 10000", rep.TotalsByCoverage["c1"])
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("violations: %v", rep.Violations)
	}
}

func TestSharedLimitAcrossCoverages(t *testing.T) {
	c1 := Coverage{ID: "a", Window: Window{Start: d("2025-01-01"), End: d("2025-12-31")}, SharedGroup: "g"}
	c2 := Coverage{ID: "b", Window: Window{Start: d("2025-01-01"), End: d("2025-12-31")}, SharedGroup: "g"}
	p := Policy{Coverages: []Coverage{c1, c2}, SharedLimits: []SharedLimit{{ID: "g", LimitCents: 1000}}}
	e := newEngine(t, p)
	if err := e.Submit(ev("x", "a", "2025-03-01", 1, 800)); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit(ev("y", "b", "2025-03-02", 2, 500)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "x"); got != 800 {
		t.Fatalf("x payable = %d, want 800", got)
	}
	if got := payable(t, e, "y"); got != 200 {
		t.Fatalf("y payable = %d, want 200", got)
	}
	if !hasReason(e, "y", ReasonSharedLimit) {
		t.Fatalf("y missing SHARED_LIMIT reason")
	}
	rep := e.Report()
	if rep.TotalsBySharedGrp["g"] != 1000 {
		t.Fatalf("shared total = %d, want 1000", rep.TotalsBySharedGrp["g"])
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("violations: %v", rep.Violations)
	}
}

// 迟到事件就诊日期更早，重算后抢占免赔额，已付事件获得追补。
func TestLateEventTriggersSupplement(t *testing.T) {
	c := baseCoverage() // 免赔 1000
	e := newEngine(t, Policy{Coverages: []Coverage{c}})
	// 先到的 e1（3 月就诊，5000）-> 免赔 1000 后付 4000。
	if err := e.Submit(ev("e1", "c1", "2025-03-01", 1, 5000)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "e1"); got != 4000 {
		t.Fatalf("e1 payable = %d, want 4000", got)
	}
	// 迟到的 e2（2 月就诊，2000）：重算后 e2 先消耗免赔 1000 付 1000，
	// e1 不再被扣免赔，付 5000 -> 追补 1000。
	if err := e.Submit(ev("e2", "c1", "2025-02-01", 2, 2000)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "e2"); got != 1000 {
		t.Fatalf("e2 payable = %d, want 1000", got)
	}
	if got := payable(t, e, "e1"); got != 5000 {
		t.Fatalf("e1 payable = %d, want 5000", got)
	}
	adjs := e.Report().Adjustments
	if len(adjs) != 1 || adjs[0].EventID != "e1" || adjs[0].Kind != AdjSupplement || adjs[0].AmountCents != 1000 {
		t.Fatalf("adjustments = %+v, want one 追补 of 1000 for e1", adjs)
	}
}

// 迟到事件抢占累计限额，已付事件被追回。
func TestLateEventTriggersClawback(t *testing.T) {
	c := baseCoverage()
	c.DeductibleCents = 0
	c.PerEventLimitCents = 0
	c.AggregateLimitCents = 5000
	e := newEngine(t, Policy{Coverages: []Coverage{c}})
	// e1（3 月就诊，5000）占满累计限额。
	if err := e.Submit(ev("e1", "c1", "2025-03-01", 1, 5000)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "e1"); got != 5000 {
		t.Fatalf("e1 payable = %d, want 5000", got)
	}
	// 迟到的 e2（2 月就诊，3000）重算后先占用限额，e1 只剩 2000 -> 追回 3000。
	if err := e.Submit(ev("e2", "c1", "2025-02-01", 2, 3000)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "e2"); got != 3000 {
		t.Fatalf("e2 payable = %d, want 3000", got)
	}
	if got := payable(t, e, "e1"); got != 2000 {
		t.Fatalf("e1 payable = %d, want 2000", got)
	}
	adjs := e.Report().Adjustments
	if len(adjs) != 1 || adjs[0].EventID != "e1" || adjs[0].Kind != AdjClawback || adjs[0].AmountCents != 3000 {
		t.Fatalf("adjustments = %+v, want one 追回 of 3000 for e1", adjs)
	}
	rep := e.Report()
	if rep.TotalsByCoverage["c1"] != 5000 {
		t.Fatalf("total = %d, want 5000", rep.TotalsByCoverage["c1"])
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("violations: %v", rep.Violations)
	}
}

// 同一就诊日期的事件按到达序号排序，保证确定性。
func TestSameDateOrderedBySeq(t *testing.T) {
	c := baseCoverage()
	c.DeductibleCents = 0
	c.PerEventLimitCents = 0
	c.AggregateLimitCents = 1000
	e := newEngine(t, Policy{Coverages: []Coverage{c}})
	if err := e.Submit(ev("first", "c1", "2025-03-01", 1, 800)); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit(ev("second", "c1", "2025-03-01", 2, 800)); err != nil {
		t.Fatal(err)
	}
	if got := payable(t, e, "first"); got != 800 {
		t.Fatalf("first payable = %d, want 800", got)
	}
	if got := payable(t, e, "second"); got != 200 {
		t.Fatalf("second payable = %d, want 200", got)
	}
}

func TestInvalidPolicyRejected(t *testing.T) {
	if _, err := NewEngine(Policy{Coverages: []Coverage{{ID: "x", SharedGroup: "missing"}}}); err == nil {
		t.Fatal("expected error for unknown shared group")
	}
	if _, err := NewEngine(Policy{Coverages: []Coverage{{ID: "x"}, {ID: "x"}}}); err == nil {
		t.Fatal("expected error for duplicate coverage id")
	}
}
