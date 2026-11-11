package discretecomp

import (
	"math/big"
	"sort"
)

// Direction selects which hockey-stick divergence is evaluated.
type Direction int

const (
	// DeltaPOverQ computes delta(P||Q), the slack of the P-side privacy bound.
	DeltaPOverQ Direction = iota
	// DeltaQOverP computes delta(Q||P), the slack of the Q-side privacy bound.
	DeltaQOverP
)

// Profile is the exact likelihood-ratio profile of the composed mechanism.
// Finite buckets cover every product outcome that has strictly positive
// probability under both distributions; zero-probability product events are
// aggregated in Zero.
type Profile struct {
	Finite []Bucket
	Zero   ZeroEvents

	// FinitePMass and FiniteQMass are the total finite-ratio masses. Together
	// with Zero they sum to one on each side.
	FinitePMass *big.Rat
	FiniteQMass *big.Rat
}

// Compose builds the exact product profile of the given independent
// mechanisms by exhaustive enumeration. The number of distinct product
// buckets grows multiplicatively, so this is intended for small mechanisms;
// use ComposeTruncated for large product spaces.
func Compose(mechanisms []*Mechanism) (*Profile, error) {
	if len(mechanisms) == 0 {
		return nil, errEmptyMechanism
	}
	for _, m := range mechanisms {
		if m == nil {
			return nil, errEmptyMechanism
		}
	}

	// Aggregate zero-probability events of the product. Under the product P
	// distribution the probability that no component lands in its P-only class
	// is prod(1 - pOnly_i); hence the P-only (ratio +Inf) mass is one minus
	// that product. Q-only mass follows the same argument on the Q side.
	pOnly := big.NewRat(1, 1)
	qOnly := big.NewRat(1, 1)
	for _, m := range mechanisms {
		pOnly.Mul(pOnly, new(big.Rat).Sub(big.NewRat(1, 1), m.Zero.POnly))
		qOnly.Mul(qOnly, new(big.Rat).Sub(big.NewRat(1, 1), m.Zero.QOnly))
	}
	pOnly.Sub(big.NewRat(1, 1), pOnly)
	qOnly.Sub(big.NewRat(1, 1), qOnly)

	type mass struct{ p, q *big.Rat }
	merged := map[string]mass{}

	// Seed with the empty product (one outcome of mass 1 on each side).
	merged["1/1"] = mass{p: big.NewRat(1, 1), q: big.NewRat(1, 1)}

	for _, m := range mechanisms {
		next := make(map[string]mass, len(merged)*max(1, len(m.Finite)))
		for _, cur := range merged {
			for _, b := range m.Finite {
				p := new(big.Rat).Mul(cur.p, b.PMass)
				q := new(big.Rat).Mul(cur.q, b.QMass)
				ratio := new(big.Rat).Quo(p, q)
				key := ratio.RatString()
				if exist, ok := next[key]; ok {
					exist.p.Add(exist.p, p)
					exist.q.Add(exist.q, q)
					next[key] = exist
				} else {
					next[key] = mass{p: p, q: q}
				}
			}
		}
		merged = next
	}

	buckets := make([]Bucket, 0, len(merged))
	totalP := new(big.Rat)
	totalQ := new(big.Rat)
	for _, v := range merged {
		buckets = append(buckets, Bucket{
			PMass: v.p,
			QMass: v.q,
			Ratio: new(big.Rat).Quo(v.p, v.q),
		})
		totalP.Add(totalP, v.p)
		totalQ.Add(totalQ, v.q)
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Ratio.Cmp(buckets[j].Ratio) > 0
	})

	return &Profile{
		Finite:      buckets,
		Zero:        ZeroEvents{POnly: pOnly, QOnly: qOnly, BothZero: big.NewRat(0, 1)},
		FinitePMass: totalP,
		FiniteQMass: totalQ,
	}, nil
}

// Delta returns the exact additive slack of approximate differential privacy
// for the multiplicative budget e^epsilon = budget (a rational number >= 1):
//
//	direction P||Q:  delta = POnly + sum_{ratio > budget} (p - budget*q)
//	direction Q||P:  delta = QOnly + sum_{ratio < 1/budget} (q - p/budget)
//
// This is the smallest delta for which the profile satisfies
// (epsilon, delta)-DP in the chosen direction.
func (pr *Profile) Delta(budget *big.Rat, dir Direction) (*big.Rat, error) {
	if budget == nil || budget.Cmp(big.NewRat(1, 1)) < 0 {
		return nil, errBadBudget
	}
	delta := new(big.Rat)
	switch dir {
	case DeltaPOverQ:
		delta.Set(pr.Zero.POnly)
		for _, b := range pr.Finite {
			if b.Ratio.Cmp(budget) > 0 {
				term := new(big.Rat).Mul(budget, b.QMass)
				term.Sub(b.PMass, term)
				delta.Add(delta, term)
			}
		}
	case DeltaQOverP:
		delta.Set(pr.Zero.QOnly)
		inv := new(big.Rat).Inv(budget)
		for _, b := range pr.Finite {
			if b.Ratio.Cmp(inv) < 0 {
				term := new(big.Rat).Mul(inv, b.PMass)
				term.Sub(b.QMass, term)
				delta.Add(delta, term)
			}
		}
	default:
		return nil, errBadBudget
	}
	return delta, nil
}
