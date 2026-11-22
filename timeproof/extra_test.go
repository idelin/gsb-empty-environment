package timeproof

import "testing"

// TestGapsAreNotFilled verifies that two separately-feasible domains produce a
// disjoint union: the hole between them stays a hole and carries independent,
// checkable exclusion evidence for every consistent mask.
func TestGapsAreNotFilled(t *testing.T) {
	clock := &Clock{Segments: []DriftSeg{
		{From: RatInt(0), To: RatInt(100), RateLo: NewRat(1, 2), RateHi: NewRat(1, 1)},
	}}
	// Domain A pins the query near 100; domain B pins it near 300. With f=1
	// each single-domain scenario is admissible, giving two separated islands.
	mk := func(domain string, send, signed int64) *Receipt {
		return &Receipt{
			Nonce: "n", Domain: domain, ServerID: "srv-" + domain,
			SendMono: RatInt(send), RecvMono: RatInt(send + 4),
			ProcessLo: RatInt(0), ProcessHi: RatInt(0), OneWayDelay: RatInt(1),
			SignedLo: RatInt(signed), SignedHi: RatInt(signed),
		}
	}
	q := &Query{
		Nonce: "n", Clock: clock, QueryMono: RatInt(20), MaxFaulty: 1,
		Receipts: []*Receipt{mk("A", 10, 100), mk("B", 10, 300)},
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Union) < 2 {
		t.Fatalf("expected >=2 disjoint islands, got %d", len(sol.Union))
	}
	if len(sol.Gaps) == 0 {
		t.Fatal("expected at least one gap, got none")
	}
	for i := 1; i < len(sol.Union); i++ {
		if sol.Union[i].Lo.Cmp(sol.Union[i-1].Hi) <= 0 {
			t.Fatal("union intervals overlap/touch; not normalized")
		}
	}
	re, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range sol.Gaps {
		if err := VerifyGap(q, g, re); err != nil {
			t.Fatalf("gap verification: %v", err)
		}
	}
	if err := sol.Verify(q); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestStateCapTruncation forces the enumeration cap to be hit and checks that
// no definite verdict is claimed.
func TestStateCapTruncation(t *testing.T) {
	clock := clock1to2()
	var receipts []*Receipt
	for d := 0; d < 5; d++ {
		r := baseReceipt()
		r.Domain = string(rune('A' + d))
		r.ServerID = "srv-" + r.Domain
		receipts = append(receipts, r)
	}
	q := &Query{
		Nonce: "n1", Clock: clock, Receipts: receipts,
		QueryMono: RatInt(20), MaxFaulty: 4, StateCap: 3,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Truncated {
		t.Fatal("expected truncated flag when state cap is hit")
	}
	cls := Classify(sol, RatInt(0), RatInt(1<<20))
	if cls.Verdict != VerdictUncertain {
		t.Fatalf("truncated result must be uncertain, got %s", cls.Verdict)
	}
}

// TestRationalArithmetic uses fractional drift, delay and processing values
// and confirms exact rational endpoints match the Floyd reference solver.
func TestRationalArithmetic(t *testing.T) {
	clock := &Clock{Segments: []DriftSeg{
		{From: RatInt(0), To: RatInt(50), RateLo: NewRat(1, 3), RateHi: NewRat(7, 5)},
	}}
	r := &Receipt{
		Nonce: "n", Domain: "A", ServerID: "s",
		SendMono: NewRat(7, 2), RecvMono: NewRat(19, 3),
		ProcessLo: NewRat(1, 4), ProcessHi: NewRat(9, 2),
		OneWayDelay: NewRat(2, 7),
		SignedLo:    NewRat(1001, 10), SignedHi: NewRat(2003, 10),
	}
	q := &Query{Nonce: "n", Clock: clock, Receipts: []*Receipt{r},
		QueryMono: NewRat(40, 3), MaxFaulty: 0}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	it := sol.Union[0]
	lo, hi, ok := floydInterval(t, q, []bool{true})
	if !ok {
		t.Fatal("reference solver infeasible")
	}
	if it.Lo.Cmp(lo) != 0 || it.Hi.Cmp(hi) != 0 {
		t.Fatalf("rational mismatch [%v,%v] vs floyd [%v,%v]", it.Lo, it.Hi, lo, hi)
	}
	if err := sol.Verify(q); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestDomainAtomicity checks that a domain is all-or-nothing: a domain with
// one good and one contradictory receipt cannot be partially retained.
func TestDomainAtomicity(t *testing.T) {
	good := baseReceipt()
	bad := baseReceipt()
	bad.ServerID = "srv-a2"
	bad.SignedLo = RatInt(5000)
	bad.SignedHi = RatInt(5000)
	// f=0 means no domain may be dropped; the only mask is inconsistent.
	q := &Query{
		Nonce: "n1", Clock: clock1to2(),
		Receipts:  []*Receipt{good, bad},
		QueryMono: RatInt(20), MaxFaulty: 0,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Outcomes) != 0 || len(sol.Conflicts) == 0 {
		t.Fatalf("atomic domain must be wholly inconsistent, outcomes=%d conflicts=%d",
			len(sol.Outcomes), len(sol.Conflicts))
	}
	for _, c := range sol.Conflicts {
		for _, ri := range c.KeptReceipts {
			if ri != 0 && ri != 1 {
				t.Fatalf("unexpected receipt index in conflict mask: %d", ri)
			}
		}
		if err := VerifyCycle(c); err != nil {
			t.Fatalf("cycle: %v", err)
		}
	}
}

// TestNoEvidenceIsUnbounded confirms that a query without receipts is simply
// unbounded (uncertain), not a conflict and not an expiry.
func TestNoEvidenceIsUnbounded(t *testing.T) {
	q := &Query{
		Nonce: "n", Clock: clock1to2(), Receipts: nil,
		QueryMono: RatInt(20), MaxFaulty: 0,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	if sol.HasEvidence {
		t.Fatal("expected no evidence")
	}
	if len(sol.Union) != 1 || sol.Union[0].Lo != nil || sol.Union[0].Hi != nil {
		t.Fatalf("expected one unbounded interval, got %+v", sol.Union)
	}
	cls := Classify(sol, RatInt(0), RatInt(10))
	if cls.Verdict != VerdictUncertain {
		t.Fatalf("no evidence must be uncertain, got %s", cls.Verdict)
	}
}

// TestReplayRejected ensures a foreign/nonce-mismatched receipt is refused.
func TestReplayRejected(t *testing.T) {
	r := baseReceipt()
	r.Nonce = "stale-nonce"
	q := &Query{
		Nonce: "n", Clock: clock1to2(), Receipts: []*Receipt{r},
		QueryMono: RatInt(20), MaxFaulty: 0,
	}
	if _, err := Analyze(q); err == nil {
		t.Fatal("expected nonce binding validation error")
	}
}
