package timeproof

import "testing"

// TestFaultToleranceUnion builds three domains, one of which is wildly wrong.
// With f=1 the two healthy domains remain admissible while the faulty one is
// recorded as a conflict; with f=0 every admissible mask is inconsistent.
func TestFaultToleranceUnion(t *testing.T) {
	clock := &Clock{Segments: []DriftSeg{
		{From: RatInt(0), To: RatInt(100), RateLo: NewRat(1, 4), RateHi: RatInt(1)},
	}}
	mk := func(domain string, signed int64) *Receipt {
		return &Receipt{
			Nonce: "n1", Domain: domain, ServerID: "srv-" + domain,
			SendMono: RatInt(10), RecvMono: RatInt(14),
			ProcessLo: RatInt(0), ProcessHi: RatInt(0), OneWayDelay: RatInt(1),
			SignedLo: RatInt(signed), SignedHi: RatInt(signed),
		}
	}
	goodA := mk("A", 100)
	goodB := mk("B", 100)
	bad1 := mk("C", 9000)
	bad2 := mk("C", 9000) // domain C is coherent but wildly wrong vs A/B

	tolerant := &Query{
		Nonce: "n1", Clock: clock, QueryMono: RatInt(20), MaxFaulty: 1,
		Receipts: []*Receipt{goodA, goodB, bad1, bad2},
	}
	sol, err := Analyze(tolerant)
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Conflicts) == 0 {
		t.Fatal("expected conflict evidence for the faulty domain")
	}
	// With f=1 at most one domain may be dropped. Keeping A alone or B alone
	// would also drop the other healthy domain (two faults total), which is
	// over budget. The only admissible consistent mask is therefore A+B,
	// dropping exactly the faulty domain C.
	if len(sol.Outcomes) != 1 {
		t.Fatalf("want 1 consistent mask (A+B), got %d", len(sol.Outcomes))
	}
	for _, c := range sol.Conflicts {
		if err := VerifyCycle(c); err != nil {
			t.Fatalf("cycle: %v", err)
		}
	}
	if err := sol.Verify(tolerant); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Without any fault budget all three domains must be kept together, which
	// is infeasible; no consistent mask survives, so this is a pure conflict.
	strict := *tolerant
	strict.MaxFaulty = 0
	solStrict, err := Analyze(&strict)
	if err != nil {
		t.Fatal(err)
	}
	if len(solStrict.Outcomes) != 0 || len(solStrict.Conflicts) == 0 {
		t.Fatalf("f=0 with a bad domain must be a pure conflict: outcomes=%d conflicts=%d",
			len(solStrict.Outcomes), len(solStrict.Conflicts))
	}
	if Classify(solStrict, RatInt(0), RatInt(1<<20)).Verdict != VerdictConflict {
		t.Fatal("strict inconsistency must classify as conflict, not expiry")
	}

	// With f=2 the single-domain survivors become admissible: A, B and C (C is
	// internally coherent), plus A+B. The all-kept mask stays a conflict.
	lenient := *tolerant
	lenient.MaxFaulty = 2
	solLenient, err := Analyze(&lenient)
	if err != nil {
		t.Fatal(err)
	}
	if len(solLenient.Outcomes) != 4 {
		t.Fatalf("want 4 consistent masks under f=2, got %d", len(solLenient.Outcomes))
	}
	if err := solLenient.Verify(&lenient); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
