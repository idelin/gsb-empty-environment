package discretecomp

import (
	"math/big"
	"testing"
)

func TestTruncatedBoundsContainExact(t *testing.T) {
	p, q := bernoulli(rat(1, 3))
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	mechs := []*Mechanism{m, m, m, m, m, m}
	profile, err := Compose(mechs)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	budgets := []*big.Rat{rat(1, 1), rat(3, 2), rat(2, 1), rat(5, 1)}
	massBudgets := []*big.Rat{rat(0, 1), rat(1, 100), rat(1, 10), rat(1, 4)}

	for _, dir := range []Direction{DeltaPOverQ, DeltaQOverP} {
		for _, budget := range budgets {
			exact, err := profile.Delta(budget, dir)
			if err != nil {
				t.Fatalf("exact Delta: %v", err)
			}
			for _, mb := range massBudgets {
				res, err := ComposeTruncated(mechs, budget, dir, TruncateOptions{MassBudget: mb})
				if err != nil {
					t.Fatalf("ComposeTruncated: %v", err)
				}
				if res.Bounds.Lower.Cmp(exact) > 0 || res.Bounds.Upper.Cmp(exact) < 0 {
					t.Fatalf("dir=%v budget=%s mb=%s: exact %s outside [%s, %s]",
						dir, budget, mb, exact, res.Bounds.Lower, res.Bounds.Upper)
				}
				if res.Bounds.Lower.Cmp(res.Bounds.Upper) > 0 {
					t.Fatalf("lower above upper")
				}
			}
		}
	}
}

func TestZeroMassBudgetIsExact(t *testing.T) {
	p, q := bernoulli(rat(1, 3))
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	mechs := []*Mechanism{m, m, m}
	profile, err := Compose(mechs)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	for _, dir := range []Direction{DeltaPOverQ, DeltaQOverP} {
		res, err := ComposeTruncated(mechs, rat(2, 1), dir, TruncateOptions{MassBudget: rat(0, 1)})
		if err != nil {
			t.Fatalf("ComposeTruncated: %v", err)
		}
		if !res.Stats.Exact || res.Bounds.Lower.Cmp(res.Bounds.Upper) != 0 {
			t.Fatalf("zero mass budget must be exact: %+v", res.Stats)
		}
		exact, err := profile.Delta(rat(2, 1), dir)
		if err != nil {
			t.Fatalf("Delta: %v", err)
		}
		if res.Bounds.Lower.Cmp(exact) != 0 {
			t.Fatalf("exact mismatch: got %s want %s", res.Bounds.Lower, exact)
		}
		if res.Stats.Buckets != len(profile.Finite) {
			t.Fatalf("bucket count %d want %d", res.Stats.Buckets, len(profile.Finite))
		}
	}
}

func TestMaxNodeCapStopsEarly(t *testing.T) {
	p, q := bernoulli(rat(1, 3))
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	mechs := []*Mechanism{m, m, m, m, m, m, m, m}
	res, err := ComposeTruncated(mechs, rat(2, 1), DeltaPOverQ,
		TruncateOptions{MassBudget: rat(0, 1), MaxNodes: 5})
	if err != nil {
		t.Fatalf("ComposeTruncated: %v", err)
	}
	if res.Stats.NodesPopped != 5 {
		t.Fatalf("popped %d nodes, want 5", res.Stats.NodesPopped)
	}
	if res.Stats.Exact {
		t.Fatalf("capped enumeration must not be reported exact")
	}
	if res.Bounds.Lower.Cmp(res.Bounds.Upper) >= 0 {
		t.Fatalf("capped bounds should be strict: [%s, %s]", res.Bounds.Lower, res.Bounds.Upper)
	}
}

func TestFullMassBucketEnumeratesNothing(t *testing.T) {
	p, q := bernoulli(rat(1, 3))
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	mechs := []*Mechanism{m, m}
	profile, err := Compose(mechs)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	res, err := ComposeTruncated(mechs, rat(2, 1), DeltaPOverQ,
		TruncateOptions{MassBudget: rat(1, 1)})
	if err != nil {
		t.Fatalf("ComposeTruncated: %v", err)
	}
	if res.Stats.NodesPopped != 0 || res.Stats.Buckets != 0 {
		t.Fatalf("full mass budget should enumerate nothing: %+v", res.Stats)
	}
	if res.Bounds.Lower.Cmp(profile.Zero.POnly) != 0 {
		t.Fatalf("lower = %s, want zero mass %s", res.Bounds.Lower, profile.Zero.POnly)
	}
	if res.Bounds.Upper.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("upper = %s, want 1", res.Bounds.Upper)
	}
}

func TestHandComputedTwoMechanism(t *testing.T) {
	// Mechanism: P=(1/3,2/3), Q=(2/3,1/3); ratios 1/2 and 2.
	p := dist("0", "1/3", "1", "2/3")
	q := dist("0", "2/3", "1", "1/3")
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	mechs := []*Mechanism{m, m}
	profile, err := Compose(mechs)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	// Four product outcomes, four distinct ratio products: 1/4, 1, 1, 4.
	// The two ratio-1 outcomes are merged, leaving 3 finite buckets.
	if len(profile.Finite) != 3 {
		t.Fatalf("got %d buckets, want 3: %+v", len(profile.Finite), profile.Finite)
	}

	// At budget 3/2 only the ratio-4 bucket contributes:
	// P(1,1)=4/9, Q(1,1)=1/9 => delta = 4/9 - (3/2)(1/9) = 8/18 - 3/18 = 5/18.
	delta, err := profile.Delta(rat(3, 2), DeltaPOverQ)
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	if delta.Cmp(rat(5, 18)) != 0 {
		t.Fatalf("delta = %s, want 5/18", delta)
	}

	// Symmetry: delta(Q||P) at the same budget equals delta(P||Q) here.
	reverse, err := profile.Delta(rat(3, 2), DeltaQOverP)
	if err != nil {
		t.Fatalf("Delta reverse: %v", err)
	}
	// Only the ratio-1/4 bucket (outcome 0,0) clears 1/budget = 2/3:
	// Q mass 4/9, P mass 1/9 => 4/9 - (2/3)(1/9) = 12/27 - 2/27 = 10/27.
	if reverse.Cmp(rat(10, 27)) != 0 {
		t.Fatalf("reverse delta = %s, want 10/27", reverse)
	}

	// A truncated run with a small mass budget must bracket the exact value.
	res, err := ComposeTruncated(mechs, rat(3, 2), DeltaPOverQ,
		TruncateOptions{MassBudget: rat(1, 2)})
	if err != nil {
		t.Fatalf("ComposeTruncated: %v", err)
	}
	if res.Bounds.Lower.Cmp(delta) > 0 || res.Bounds.Upper.Cmp(delta) < 0 {
		t.Fatalf("exact %s outside [%s, %s]", delta, res.Bounds.Lower, res.Bounds.Upper)
	}
}
