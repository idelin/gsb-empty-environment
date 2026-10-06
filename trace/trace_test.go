package trace

import (
	"math/big"
	"testing"
)

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func checkFraction(t *testing.T, l *Ledger, source, batch string, want *big.Rat) {
	t.Helper()
	got := l.Batch(batch).Fraction(source)
	if got.Cmp(want) != 0 {
		t.Errorf("fraction of %s in %s = %s, want %s", source, batch, got.RatString(), want.RatString())
	}
}

func verifyAll(t *testing.T, l *Ledger, sources ...string) {
	t.Helper()
	for _, s := range sources {
		if err := l.VerifyConservation(s); err != nil {
			t.Errorf("conservation: %v", err)
		}
		if err := l.VerifyAgainstPaths(s); err != nil {
			t.Errorf("path cross-check: %v", err)
		}
	}
}

func TestMixSplitDiscardQuery(t *testing.T) {
	l := NewLedger()
	must(t, l.AddSource("S1", rat(100, 1)))
	must(t, l.AddSource("S2", rat(200, 1)))

	must(t, l.Mix("A",
		Portion{"S1", rat(30, 1)},
		Portion{"S2", rat(60, 1)},
	))
	checkFraction(t, l, "S1", "A", rat(1, 3))
	checkFraction(t, l, "S2", "A", rat(2, 3))

	must(t, l.Split("B", "A", rat(30, 1)))
	checkFraction(t, l, "S1", "B", rat(1, 3))

	must(t, l.Mix("C",
		Portion{"A", rat(10, 1)},
		Portion{"B", rat(5, 1)},
	))
	checkFraction(t, l, "S1", "C", rat(1, 3))

	must(t, l.Discard("B", rat(5, 1)))

	aff := l.AffectedBy("S1")
	want := map[string]*big.Rat{"S1": rat(1, 1), "A": rat(1, 3), "B": rat(1, 3), "C": rat(1, 3)}
	if len(aff) != len(want) {
		t.Fatalf("AffectedBy(S1) = %v, want keys %v", aff, want)
	}
	for id, w := range want {
		if aff[id].Cmp(w) != 0 {
			t.Errorf("AffectedBy(S1)[%s] = %s, want %s", id, aff[id].RatString(), w.RatString())
		}
	}
	if _, ok := aff["S2"]; ok {
		t.Errorf("S2 must not appear in AffectedBy(S1)")
	}

	if got := l.MassFrom("S1", "C"); got.Cmp(rat(5, 1)) != 0 {
		t.Errorf("MassFrom(S1, C) = %s, want 5", got.RatString())
	}
	if got := l.DiscardedMass("S1"); got.Cmp(rat(5, 3)) != 0 {
		t.Errorf("DiscardedMass(S1) = %s, want 5/3", got.RatString())
	}

	verifyAll(t, l, "S1", "S2")
}

func TestDiamondSharedPathsDeduplicated(t *testing.T) {
	l := NewLedger()
	must(t, l.AddSource("S", rat(12, 1)))
	must(t, l.AddSource("CLEAN", rat(12, 1)))

	must(t, l.Mix("A",
		Portion{"S", rat(6, 1)},
		Portion{"CLEAN", rat(6, 1)},
	))
	must(t, l.Split("B1", "A", rat(3, 1)))
	must(t, l.Split("B2", "A", rat(3, 1)))
	must(t, l.Mix("D",
		Portion{"B1", rat(3, 1)},
		Portion{"B2", rat(3, 1)},
	))

	checkFraction(t, l, "S", "D", rat(1, 2))
	if got := l.MassFrom("S", "D"); got.Cmp(rat(3, 1)) != 0 {
		t.Errorf("MassFrom(S, D) = %s, want 3 (shared edge S->A counted once)", got.RatString())
	}

	aff := l.AffectedBy("S")
	want := map[string]*big.Rat{
		"S": rat(1, 1), "A": rat(1, 2),
		"B1": rat(1, 2), "B2": rat(1, 2), "D": rat(1, 2),
	}
	if len(aff) != len(want) {
		t.Fatalf("AffectedBy(S) = %v, want keys %v", aff, want)
	}
	for id, w := range want {
		if aff[id].Cmp(w) != 0 {
			t.Errorf("AffectedBy(S)[%s] = %s, want %s", id, aff[id].RatString(), w.RatString())
		}
	}
	if _, ok := aff["CLEAN"]; ok {
		t.Errorf("CLEAN must not be affected by S")
	}

	verifyAll(t, l, "S", "CLEAN")
}

func TestZeroContributionDoesNotPropagate(t *testing.T) {
	l := NewLedger()
	must(t, l.AddSource("TOXIC", rat(10, 1)))
	must(t, l.AddSource("PURE", rat(10, 1)))

	must(t, l.Mix("A",
		Portion{"TOXIC", rat(0, 1)},
		Portion{"PURE", rat(5, 1)},
	))
	if got := l.Batch("A").Fraction("TOXIC"); got.Sign() != 0 {
		t.Errorf("zero-amount portion tainted A: fraction %s", got.RatString())
	}
	if _, ok := l.AffectedBy("TOXIC")["A"]; ok {
		t.Errorf("A must not appear in AffectedBy(TOXIC)")
	}

	must(t, l.Mix("B",
		Portion{"A", rat(3, 1)},
		Portion{"TOXIC", rat(2, 1)},
	))
	checkFraction(t, l, "TOXIC", "B", rat(2, 5))

	if err := l.Mix("EMPTY", Portion{"PURE", rat(0, 1)}); err == nil {
		t.Errorf("all-zero mix must be rejected")
	}

	verifyAll(t, l, "TOXIC", "PURE")
}

func TestCycleAndReuseRejected(t *testing.T) {
	l := NewLedger()
	must(t, l.AddSource("A", rat(10, 1)))
	must(t, l.Mix("B", Portion{"A", rat(4, 1)}))

	if err := l.Mix("A", Portion{"B", rat(1, 1)}); err == nil {
		t.Errorf("reusing existing id A as mix output must be rejected")
	}
	if err := l.Split("B", "A", rat(1, 1)); err == nil {
		t.Errorf("reusing existing id B as split output must be rejected")
	}
	if err := l.Mix("C", Portion{"GHOST", rat(1, 1)}); err == nil {
		t.Errorf("unknown input batch must be rejected")
	}
	if err := l.AddSource("A", rat(1, 1)); err == nil {
		t.Errorf("duplicate source id must be rejected")
	}
}

func TestConservationWithDiscardAndPartialDraws(t *testing.T) {
	l := NewLedger()
	must(t, l.AddSource("S", rat(100, 1)))
	must(t, l.AddSource("W", rat(50, 1)))

	must(t, l.Split("P", "S", rat(40, 1)))
	must(t, l.Discard("P", rat(7, 1)))
	must(t, l.Mix("M",
		Portion{"P", rat(33, 1)},
		Portion{"S", rat(55, 1)},
		Portion{"W", rat(7, 1)},
	))
	must(t, l.Discard("M", rat(25, 2)))
	must(t, l.Discard("S", rat(1, 2)))

	checkFraction(t, l, "S", "M", rat(88, 95))
	checkFraction(t, l, "W", "M", rat(7, 95))

	verifyAll(t, l, "S", "W")
}

func TestOverdrawAndInvalidAmounts(t *testing.T) {
	l := NewLedger()
	must(t, l.AddSource("S", rat(5, 1)))

	if err := l.Split("X", "S", rat(6, 1)); err == nil {
		t.Errorf("overdraw split must be rejected")
	}
	if err := l.Discard("S", rat(0, 1)); err == nil {
		t.Errorf("zero discard must be rejected")
	}
	if err := l.Mix("Y", Portion{"S", rat(-1, 1)}); err == nil {
		t.Errorf("negative portion must be rejected")
	}
	if err := l.AddSource("Z", rat(0, 1)); err == nil {
		t.Errorf("zero-mass source must be rejected")
	}
	must(t, l.Split("X", "S", rat(5, 1)))
	if err := l.Discard("S", rat(1, 1)); err == nil {
		t.Errorf("discard from fully drained batch must be rejected")
	}
	verifyAll(t, l, "S")
}
