package discretecomp

import (
	"math/big"
	"testing"
)

func TestNewMechanismMergesEqualRatios(t *testing.T) {
	// Two outcomes share ratio 1/2 and must be merged into a single bucket.
	p := dist("a", "1/6", "b", "1/6", "c", "2/3")
	q := dist("a", "1/3", "b", "1/3", "c", "1/3")
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	if len(m.Finite) != 2 {
		t.Fatalf("got %d finite buckets, want 2", len(m.Finite))
	}
	// Decreasing ratio order: (2/3)/(1/3) = 2, then the merged 1/2 class.
	if m.Finite[0].Ratio.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("first ratio = %s, want 2", m.Finite[0].Ratio.RatString())
	}
	merged := m.Finite[1]
	if merged.Ratio.Cmp(rat(1, 2)) != 0 ||
		merged.PMass.Cmp(rat(1, 3)) != 0 ||
		merged.QMass.Cmp(rat(2, 3)) != 0 {
		t.Fatalf("merged bucket wrong: %+v", merged)
	}
}

func TestNewMechanismValidation(t *testing.T) {
	if _, err := NewMechanism(nil, dist("a", "1/1")); err != errNilDistribution {
		t.Fatalf("nil check: %v", err)
	}
	if _, err := NewMechanism(dist("a", "1/2"), dist("a", "1/2", "b", "1/2")); err != errDifferentSupport {
		t.Fatalf("support check: %v", err)
	}
	if _, err := NewMechanism(
		dist("a", "1/3", "b", "1/3"),
		dist("a", "1/2", "b", "1/2"),
	); err != errNotNormalized {
		t.Fatalf("normalization check: %v", err)
	}
}

func TestZeroProbabilityEvents(t *testing.T) {
	// x: positive under both; y: P only (+Inf); z: Q only (ratio 0).
	p := dist("x", "1/2", "y", "1/2", "z", "0/1")
	q := dist("x", "1/4", "y", "0/1", "z", "3/4")
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	if len(m.Finite) != 1 || m.Finite[0].Ratio.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("finite buckets: %+v", m.Finite)
	}
	if m.Zero.POnly.Cmp(rat(1, 2)) != 0 || m.Zero.QOnly.Cmp(rat(3, 4)) != 0 {
		t.Fatalf("zero events wrong: %+v", m.Zero)
	}

	profile, err := Compose([]*Mechanism{m, m})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	// P-only product mass: 1 - (1 - 1/2)^2 = 3/4.
	if profile.Zero.POnly.Cmp(rat(3, 4)) != 0 {
		t.Fatalf("product POnly = %s, want 3/4", profile.Zero.POnly)
	}
	// Q-only product mass: 1 - (1 - 3/4)^2 = 15/16.
	if profile.Zero.QOnly.Cmp(rat(15, 16)) != 0 {
		t.Fatalf("product QOnly = %s, want 15/16", profile.Zero.QOnly)
	}

	// At budget 1 (epsilon = 0) delta must include every P-only event.
	delta, err := profile.Delta(rat(1, 1), DeltaPOverQ)
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	if delta.Cmp(rat(3, 4)) < 0 {
		t.Fatalf("delta(0) = %s must be >= POnly 3/4", delta)
	}
}

func TestExactMechanismIsPureDP(t *testing.T) {
	p := dist("0", "3/4", "1", "1/4")
	q := dist("0", "1/2", "1", "1/2")
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	profile, err := Compose([]*Mechanism{m, m, m})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	// Largest finite ratio is (3/2)^3 = 27/8; at that budget delta is zero.
	budget := rat(27, 8)
	delta, err := profile.Delta(budget, DeltaPOverQ)
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	if delta.Sign() != 0 {
		t.Fatalf("delta at the max ratio must be 0, got %s", delta)
	}

	// Just below the max ratio the single extreme bucket contributes slack.
	below := rat(4, 1)
	d2, err := profile.Delta(below, DeltaPOverQ)
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	// Outcome (1,1,1): P mass (1/4)^3 = 1/64, Q mass (1/2)^3 = 1/8.
	// P - budget*Q = 1/64 - 4/8 < 0, so hockey-stick contributes nothing and
	// the exact delta stays zero (no finite bucket clears budget 4).
	if d2.Sign() != 0 {
		t.Fatalf("delta = %s, want 0", d2)
	}

	// At budget 1 every finite bucket with ratio > 1 contributes P-Q.
	d3, err := profile.Delta(rat(1, 1), DeltaPOverQ)
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	// Total P mass of ratio (3/2)^k buckets for k>0 equals P(>0 ones) minus
	// their Q mass; compute directly from buckets and check against sum.
	want3 := big.NewRat(0, 1)
	for _, b := range profile.Finite {
		if b.Ratio.Cmp(rat(1, 1)) > 0 {
			want3.Add(want3, new(big.Rat).Sub(b.PMass, b.QMass))
		}
	}
	if d3.Cmp(want3) != 0 {
		t.Fatalf("delta(0) = %s, want %s", d3, want3)
	}
}

func TestProfileMassesNormalize(t *testing.T) {
	p, q := bernoulli(rat(1, 3))
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	mechs := []*Mechanism{m, m, m, m}
	profile, err := Compose(mechs)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	one := big.NewRat(1, 1)
	pSum := new(big.Rat).Add(profile.FinitePMass, profile.Zero.POnly)
	qSum := new(big.Rat).Add(profile.FiniteQMass, profile.Zero.QOnly)
	if pSum.Cmp(one) != 0 || qSum.Cmp(one) != 0 {
		t.Fatalf("masses do not normalize: P=%s Q=%s", pSum, qSum)
	}

	for _, b := range profile.Finite {
		if b.PMass.Sign() <= 0 || b.QMass.Sign() <= 0 {
			t.Fatalf("non-positive bucket mass: %+v", b)
		}
		if new(big.Rat).Quo(b.PMass, b.QMass).Cmp(b.Ratio) != 0 {
			t.Fatalf("bucket ratio inconsistent: %+v", b)
		}
	}
}

func TestInvalidArguments(t *testing.T) {
	p, q := bernoulli(rat(1, 3))
	m, err := NewMechanism(p, q)
	if err != nil {
		t.Fatalf("NewMechanism: %v", err)
	}
	mechs := []*Mechanism{m}

	if _, err := Compose(nil); err != errEmptyMechanism {
		t.Fatalf("Compose empty: %v", err)
	}
	profile, err := Compose(mechs)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, err := profile.Delta(rat(1, 2), DeltaPOverQ); err != errBadBudget {
		t.Fatalf("sub-unit budget: %v", err)
	}
	if _, err := ComposeTruncated(mechs, rat(1, 2), DeltaPOverQ, TruncateOptions{}); err != errBadBudget {
		t.Fatalf("truncate sub-unit budget: %v", err)
	}
	if _, err := ComposeTruncated(mechs, rat(1, 1), DeltaPOverQ,
		TruncateOptions{MassBudget: rat(2, 1)}); err != errBadMassBudget {
		t.Fatalf("mass budget > 1: %v", err)
	}
	if _, err := ComposeTruncated(mechs, rat(1, 1), DeltaPOverQ,
		TruncateOptions{MaxNodes: -1}); err != errBadNodeCap {
		t.Fatalf("negative node cap: %v", err)
	}
	if _, err := ComposeTruncated(mechs, rat(1, 1), Direction(99), TruncateOptions{}); err != errBadBudget {
		t.Fatalf("bad direction: %v", err)
	}
}
