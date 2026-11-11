package discretecomp

import (
	"math/big"
	"math/rand"
	"testing"
)

// randomMechanism builds a normalized pair of rational distributions over an
// alphabet of n outcomes, occasionally forcing zero probabilities so that the
// P-only / Q-only code paths are exercised.
func randomMechanism(rng *rand.Rand, n int) *Mechanism {
	rawP := make([]int64, n)
	rawQ := make([]int64, n)
	var sumP, sumQ int64
	for i := 0; i < n; i++ {
		rawP[i] = int64(rng.Intn(9) + 1)
		rawQ[i] = int64(rng.Intn(9) + 1)
		if rng.Intn(4) == 0 {
			rawP[i] = 0
		}
		if rng.Intn(4) == 0 {
			rawQ[i] = 0
		}
		sumP += rawP[i]
		sumQ += rawQ[i]
	}
	// Guarantee at least one positive mass on each side.
	if sumP == 0 {
		rawP[0], sumP = 1, 1
	}
	if sumQ == 0 {
		rawQ[0], sumQ = 1, 1
	}

	p := Distribution{}
	q := Distribution{}
	for i, w := range rawP {
		key := string(rune('a' + i))
		p[key] = big.NewRat(w, sumP)
		q[key] = big.NewRat(rawQ[i], sumQ)
	}
	m, err := NewMechanism(p, q)
	if err != nil {
		panic(err)
	}
	return m
}

func TestRandomTruncationMatchesExact(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	massBudgets := []*big.Rat{
		rat(0, 1), rat(1, 50), rat(1, 10), rat(1, 3), rat(1, 2), rat(9, 10),
	}

	for trial := 0; trial < 60; trial++ {
		k := rng.Intn(3) + 1
		mechs := make([]*Mechanism, k)
		for i := range mechs {
			mechs[i] = randomMechanism(rng, rng.Intn(3)+2)
		}

		profile, err := Compose(mechs)
		if err != nil {
			t.Fatalf("trial %d Compose: %v", trial, err)
		}

		// Budgets straddle 1 and extend past the largest finite ratio.
		budgets := []*big.Rat{
			rat(1, 1),
			big.NewRat(int64(rng.Intn(5)+2), int64(rng.Intn(2)+1)),
			big.NewRat(int64(rng.Intn(20)+2), 1),
		}

		for _, dir := range []Direction{DeltaPOverQ, DeltaQOverP} {
			for _, budget := range budgets {
				exact, err := profile.Delta(budget, dir)
				if err != nil {
					t.Fatalf("trial %d Delta: %v", trial, err)
				}
				for _, mb := range massBudgets {
					res, err := ComposeTruncated(mechs, budget, dir,
						TruncateOptions{MassBudget: mb})
					if err != nil {
						t.Fatalf("trial %d truncate: %v", trial, err)
					}
					if res.Bounds.Lower.Cmp(exact) > 0 || res.Bounds.Upper.Cmp(exact) < 0 {
						t.Fatalf("trial %d dir %v budget %s mb %s: exact %s outside [%s,%s]",
							trial, dir, budget, mb, exact, res.Bounds.Lower, res.Bounds.Upper)
					}
					// Interval width must never exceed the declared remaining mass.
					width := new(big.Rat).Sub(res.Bounds.Upper, res.Bounds.Lower)
					if width.Cmp(res.Stats.RemainingMass) != 0 {
						t.Fatalf("trial %d: width %s != remaining %s",
							trial, width, res.Stats.RemainingMass)
					}
					if mb.Sign() == 0 && !res.Stats.Exact {
						t.Fatalf("trial %d: zero mass budget must be exact", trial)
					}
				}
			}
		}
	}
}

func TestRandomNodeCapBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	for trial := 0; trial < 30; trial++ {
		k := rng.Intn(4) + 2
		mechs := make([]*Mechanism, k)
		for i := range mechs {
			mechs[i] = randomMechanism(rng, rng.Intn(3)+2)
		}
		profile, err := Compose(mechs)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		cap := rng.Intn(4) + 1
		res, err := ComposeTruncated(mechs, rat(2, 1), DeltaPOverQ,
			TruncateOptions{MassBudget: rat(0, 1), MaxNodes: cap})
		if err != nil {
			t.Fatalf("truncate: %v", err)
		}
		if res.Stats.NodesPopped > cap {
			t.Fatalf("popped %d > cap %d", res.Stats.NodesPopped, cap)
		}
		exact, err := profile.Delta(rat(2, 1), DeltaPOverQ)
		if err != nil {
			t.Fatalf("Delta: %v", err)
		}
		if res.Bounds.Lower.Cmp(exact) > 0 || res.Bounds.Upper.Cmp(exact) < 0 {
			t.Fatalf("trial %d: exact %s outside [%s,%s]",
				trial, exact, res.Bounds.Lower, res.Bounds.Upper)
		}
	}
}
