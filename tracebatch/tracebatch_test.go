package tracebatch

import (
	"math/big"
	"testing"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

func mustRegister(t *testing.T, l *Ledger, id string, qty *big.Rat) {
	t.Helper()
	if err := l.Register(id, qty); err != nil {
		t.Fatalf("Register(%s): %v", id, err)
	}
}

func mustMix(t *testing.T, l *Ledger, id string, inputs ...Contribution) {
	t.Helper()
	if err := l.Mix(id, inputs); err != nil {
		t.Fatalf("Mix(%s): %v", id, err)
	}
}

func mustSplit(t *testing.T, l *Ledger, src string, parts ...SplitPart) {
	t.Helper()
	if err := l.Split(src, parts); err != nil {
		t.Fatalf("Split(%s): %v", src, err)
	}
}

func affectedMap(t *testing.T, l *Ledger, source string) map[string]Affected {
	t.Helper()
	aff, err := l.AffectedProportions(source)
	if err != nil {
		t.Fatalf("AffectedProportions(%s): %v", source, err)
	}
	m := make(map[string]Affected, len(aff))
	for _, a := range aff {
		if _, dup := m[a.BatchID]; dup {
			t.Fatalf("batch %s reported twice (shared path not deduplicated)", a.BatchID)
		}
		m[a.BatchID] = a
	}
	return m
}

func expectProp(t *testing.T, m map[string]Affected, id string, want *big.Rat) {
	t.Helper()
	a, ok := m[id]
	if !ok {
		t.Fatalf("batch %s missing from affected set", id)
	}
	if a.Proportion.Cmp(want) != 0 {
		t.Fatalf("batch %s: proportion %s, want %s", id, a.Proportion.RatString(), want.RatString())
	}
}

func TestMixAndSplitProportions(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "S", rat(100, 1))
	mustRegister(t, l, "W", rat(900, 1))
	mustMix(t, l, "B",
		Contribution{Source: "S", Amount: rat(25, 1)},
		Contribution{Source: "W", Amount: rat(75, 1)},
	)
	mustSplit(t, l, "B", SplitPart{ID: "X", Amount: rat(40, 1)}, SplitPart{ID: "Y", Amount: rat(60, 1)})

	m := affectedMap(t, l, "S")
	if len(m) != 3 {
		t.Fatalf("expected 3 affected batches, got %d", len(m))
	}
	expectProp(t, m, "B", rat(1, 4))
	expectProp(t, m, "X", rat(1, 4))
	expectProp(t, m, "Y", rat(1, 4))

	if err := l.VerifyTrace("S"); err != nil {
		t.Fatalf("VerifyTrace: %v", err)
	}
	if err := l.CheckConservation(); err != nil {
		t.Fatalf("CheckConservation: %v", err)
	}
	if err := l.ValidateDAG(); err != nil {
		t.Fatalf("ValidateDAG: %v", err)
	}
}

// Diamond with shared path prefixes: S -> A,B -> D,E -> F.
func TestDiamondSharedPathsDeduplicated(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "S", rat(100, 1))
	mustRegister(t, l, "C", rat(1000, 1))
	mustSplit(t, l, "S", SplitPart{ID: "A", Amount: rat(60, 1)}, SplitPart{ID: "B", Amount: rat(40, 1)})
	mustMix(t, l, "D",
		Contribution{Source: "A", Amount: rat(30, 1)},
		Contribution{Source: "B", Amount: rat(10, 1)},
		Contribution{Source: "C", Amount: rat(60, 1)},
	)
	mustMix(t, l, "E",
		Contribution{Source: "A", Amount: rat(30, 1)},
		Contribution{Source: "B", Amount: rat(30, 1)},
	)
	mustMix(t, l, "F",
		Contribution{Source: "D", Amount: rat(50, 1)},
		Contribution{Source: "E", Amount: rat(30, 1)},
	)

	m := affectedMap(t, l, "S")
	expectProp(t, m, "A", rat(1, 1))
	expectProp(t, m, "B", rat(1, 1))
	expectProp(t, m, "D", rat(2, 5)) // (30+10)/100
	expectProp(t, m, "E", rat(1, 1)) // (30+30)/60
	expectProp(t, m, "F", rat(5, 8)) // (50*2/5 + 30*1)/80
	if _, ok := m["C"]; ok {
		t.Fatalf("clean batch C must not be affected")
	}

	if err := l.VerifyTrace("S"); err != nil {
		t.Fatalf("VerifyTrace: %v", err)
	}
	if err := l.CheckConservation(); err != nil {
		t.Fatalf("CheckConservation: %v", err)
	}
}

func TestZeroContributionRejected(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "S", rat(10, 1))
	mustRegister(t, l, "W", rat(10, 1))
	if err := l.Mix("M", []Contribution{{Source: "S", Amount: rat(0, 1)}}); err != ErrNonPositiveQuantity {
		t.Fatalf("zero amount: got %v, want ErrNonPositiveQuantity", err)
	}
	if err := l.Mix("M", []Contribution{{Source: "S", Amount: rat(-1, 2)}}); err != ErrNonPositiveQuantity {
		t.Fatalf("negative amount: got %v, want ErrNonPositiveQuantity", err)
	}
	if err := l.Split("S", []SplitPart{{ID: "P", Amount: rat(0, 1)}}); err != ErrNonPositiveQuantity {
		t.Fatalf("zero split: got %v, want ErrNonPositiveQuantity", err)
	}
	if m := affectedMap(t, l, "S"); len(m) != 0 {
		t.Fatalf("zero contribution must not propagate, got %v", m)
	}
	if err := l.CheckConservation(); err != nil {
		t.Fatalf("CheckConservation: %v", err)
	}
}

func TestCycleRejected(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "A", rat(10, 1))
	if err := l.Mix("B", []Contribution{{Source: "B", Amount: rat(1, 1)}}); err == nil {
		t.Fatalf("self-contribution must be rejected as a cycle")
	}
	if err := l.Split("A", []SplitPart{{ID: "A", Amount: rat(1, 1)}}); err == nil {
		t.Fatalf("split into self must be rejected as a cycle")
	}
	// Corrupt the graph manually to prove ValidateDAG catches real cycles.
	l.children["A"] = append(l.children["A"], outEdge{to: "A", amt: rat(1, 1)})
	if err := l.ValidateDAG(); err != ErrCycle {
		t.Fatalf("ValidateDAG: got %v, want ErrCycle", err)
	}
}

func TestScrap(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "S", rat(100, 1))
	mustSplit(t, l, "S", SplitPart{ID: "A", Amount: rat(40, 1)})
	if err := l.Scrap("A"); err != nil {
		t.Fatalf("Scrap: %v", err)
	}
	if err := l.CheckConservation(); err != nil {
		t.Fatalf("CheckConservation after scrap: %v", err)
	}
	if _, err := l.Quantity("A"); err != nil {
		t.Fatalf("Quantity: %v", err)
	}
	if err := l.Mix("M", []Contribution{{Source: "A", Amount: rat(1, 1)}}); err == nil {
		t.Fatalf("drawing from scrapped batch must fail")
	}
	if err := l.Scrap("A"); err == nil {
		t.Fatalf("double scrap must fail")
	}
	m := affectedMap(t, l, "S")
	a, ok := m["A"]
	if !ok || !a.Scrapped {
		t.Fatalf("scrapped batch A must be reported as scrapped, got %+v", m)
	}
	expectProp(t, m, "A", rat(1, 1))
}

func TestConservationDetectsCorruption(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "S", rat(100, 1))
	mustRegister(t, l, "W", rat(50, 1))
	mustMix(t, l, "M",
		Contribution{Source: "S", Amount: rat(30, 1)},
		Contribution{Source: "W", Amount: rat(10, 1)},
	)
	mustSplit(t, l, "M", SplitPart{ID: "P", Amount: rat(16, 1)})
	if err := l.Scrap("P"); err != nil {
		t.Fatalf("Scrap: %v", err)
	}
	if err := l.CheckConservation(); err != nil {
		t.Fatalf("CheckConservation: %v", err)
	}
	// Corrupt one composition entry: conservation must now fail.
	l.batches["M"].comp["S"].Add(l.batches["M"].comp["S"], rat(1, 1))
	if err := l.CheckConservation(); err == nil {
		t.Fatalf("corrupted composition must break conservation")
	}
}

func TestIntermediateSourceAndComposition(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "S", rat(100, 1))
	mustRegister(t, l, "W", rat(100, 1))
	mustMix(t, l, "M",
		Contribution{Source: "S", Amount: rat(50, 1)},
		Contribution{Source: "W", Amount: rat(50, 1)},
	)
	mustSplit(t, l, "M", SplitPart{ID: "P", Amount: rat(20, 1)})

	// Trace from the intermediate batch M: everything in P came from M.
	m := affectedMap(t, l, "M")
	expectProp(t, m, "P", rat(1, 1))
	// Trace from the origin S: half of M and half of P.
	m = affectedMap(t, l, "S")
	expectProp(t, m, "M", rat(1, 2))
	expectProp(t, m, "P", rat(1, 2))

	comp, err := l.Composition("P")
	if err != nil {
		t.Fatalf("Composition: %v", err)
	}
	if comp["S"].Cmp(rat(10, 1)) != 0 || comp["W"].Cmp(rat(10, 1)) != 0 {
		t.Fatalf("P composition = %v, want S:10 W:10", comp)
	}
	if err := l.VerifyTrace("M"); err != nil {
		t.Fatalf("VerifyTrace(M): %v", err)
	}
	if err := l.VerifyTrace("S"); err != nil {
		t.Fatalf("VerifyTrace(S): %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "S", rat(10, 1))
	if err := l.Register("S", rat(1, 1)); err == nil {
		t.Fatalf("duplicate register must fail")
	}
	if err := l.Register("", rat(1, 1)); err != ErrEmptyID {
		t.Fatalf("empty id: got %v", err)
	}
	if err := l.Mix("M", []Contribution{{Source: "S", Amount: rat(11, 1)}}); err == nil {
		t.Fatalf("overdraw must fail")
	}
	if err := l.Mix("M", []Contribution{{Source: "ghost", Amount: rat(1, 1)}}); err == nil {
		t.Fatalf("unknown source must fail")
	}
	if err := l.Mix("M", []Contribution{{Source: "S", Amount: rat(1, 1)},
		{Source: "S", Amount: rat(1, 1)},
	}); err == nil {
		t.Fatalf("duplicate source must fail")
	}
	if err := l.Mix("M", nil); err != ErrNoInputs {
		t.Fatalf("empty mix: got %v", err)
	}
	if err := l.Split("S", []SplitPart{{ID: "P1", Amount: rat(6, 1)},
		{ID: "P2", Amount: rat(6, 1)},
	}); err == nil {
		t.Fatalf("split exceeding remaining quantity must fail")
	}
	if _, err := l.AffectedProportions("ghost"); err == nil {
		t.Fatalf("unknown contamination source must fail")
	}
	// Failed operations must leave the ledger untouched.
	if err := l.CheckConservation(); err != nil {
		t.Fatalf("CheckConservation: %v", err)
	}
	q, _ := l.Quantity("S")
	if q.Cmp(rat(10, 1)) != 0 {
		t.Fatalf("S quantity = %s, want 10", q.RatString())
	}
}
