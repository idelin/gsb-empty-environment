package discretecomp

import (
	"errors"
	"math/big"
	"sort"
)

// Outcome identifies a discrete output of a mechanism. Outcomes are compared by
// value within a single mechanism only; the package never assumes anything
// about their structure.
type Outcome = string

// Distribution maps an outcome to its exact rational probability.
type Distribution map[Outcome]*big.Rat

// RatioInfinity marks a bucket whose probability is positive under P and zero
// under Q (likelihood ratio +Infinity).
var RatioInfinity = (*big.Rat)(nil)

// Bucket is one likelihood-ratio class of a mechanism.
//
// PMass and QMass are the total probabilities of every outcome in the class
// under the two neighbouring data sets. Ratio is PMass/QMass and equals
// RatioInfinity when QMass is zero while PMass is positive. A bucket with
// PMass equal to zero (ratio zero) keeps an explicit, non-nil Ratio of 0.
type Bucket struct {
	PMass *big.Rat
	QMass *big.Rat
	Ratio *big.Rat
}

// ZeroEvents collects the probability mass carried by outcomes that have zero
// probability under one of the two distributions. The three fields are
// disjoint and together describe every "infinite" or "zero" likelihood-ratio
// class; finite-ratio mass is reported separately.
type ZeroEvents struct {
	// BothZero is the mass of outcomes impossible under both P and Q. Such
	// outcomes are always zero and normally absent, but a distribution may
	// list them explicitly.
	BothZero *big.Rat
	// POnly is the mass under P of outcomes with Q(x) = 0 (ratio +Inf).
	POnly *big.Rat
	// QOnly is the mass under Q of outcomes with P(x) = 0 (ratio 0).
	QOnly *big.Rat
}

// Mechanism is one randomised mechanism reduced to its likelihood-ratio
// buckets. Finite buckets hold strictly positive mass under both
// distributions; all zero-probability mass is reported in Zero.
type Mechanism struct {
	// Finite contains the finite, strictly positive likelihood-ratio buckets.
	// The slice is sorted by decreasing ratio.
	Finite []Bucket
	// Zero collects the zero-probability events separately.
	Zero ZeroEvents
}

var (
	errNilDistribution  = errors.New("discretecomp: distribution must not be nil")
	errNegativeOrZero   = errors.New("discretecomp: probabilities must be non-negative")
	errNotNormalized    = errors.New("discretecomp: distribution probabilities must sum to one")
	errDifferentSupport = errors.New("discretecomp: the two distributions must use the same support")
	errEmptyMechanism   = errors.New("discretecomp: at least one mechanism is required")
	errBadBudget        = errors.New("discretecomp: multiplicative budget must be >= 1")
	errBadMassBudget    = errors.New("discretecomp: mass budget must be in [0, 1]")
	errBadNodeCap       = errors.New("discretecomp: node cap must be positive when set")
)

// NewMechanism reduces a pair of rational distributions for one mechanism into
// likelihood-ratio buckets. p and q must share the same support, use only
// non-negative probabilities and each sum to exactly one.
//
// Outcomes with the same ratio P(x)/Q(x) are merged by summing their masses.
// Zero-probability outcomes never enter Finite and are reported through Zero
// instead. The returned Finite buckets are sorted by decreasing ratio.
func NewMechanism(p, q Distribution) (*Mechanism, error) {
	if p == nil || q == nil {
		return nil, errNilDistribution
	}
	if len(p) != len(q) {
		return nil, errDifferentSupport
	}

	sumP := new(big.Rat)
	sumQ := new(big.Rat)

	type merged struct {
		p, q *big.Rat
	}
	finite := make(map[Outcome]merged)

	var (
		bothZero = new(big.Rat)
		pOnly    = new(big.Rat)
		qOnly    = new(big.Rat)
	)

	for x, px := range p {
		qx, ok := q[x]
		if !ok {
			return nil, errDifferentSupport
		}
		if px == nil || qx == nil || px.Sign() < 0 || qx.Sign() < 0 {
			return nil, errNegativeOrZero
		}
		sumP.Add(sumP, px)
		sumQ.Add(sumQ, qx)

		switch {
		case px.Sign() == 0 && qx.Sign() == 0:
			// No mass on either side; nothing to accumulate beyond bookkeeping.
			bothZero.Add(bothZero, big.NewRat(0, 1))
		case qx.Sign() == 0:
			pOnly.Add(pOnly, px)
		case px.Sign() == 0:
			qOnly.Add(qOnly, qx)
		default:
			ratio := new(big.Rat).Quo(px, qx)
			key := ratio.RatString()
			if cur, ok := finite[key]; ok {
				cur.p.Add(cur.p, px)
				cur.q.Add(cur.q, qx)
				finite[key] = cur
			} else {
				finite[key] = merged{p: new(big.Rat).Set(px), q: new(big.Rat).Set(qx)}
			}
		}
	}

	// Every q outcome must also appear in p.
	for x := range q {
		if _, ok := p[x]; !ok {
			return nil, errDifferentSupport
		}
	}

	one := big.NewRat(1, 1)
	if sumP.Cmp(one) != 0 || sumQ.Cmp(one) != 0 {
		return nil, errNotNormalized
	}

	buckets := make([]Bucket, 0, len(finite))
	for _, m := range finite {
		buckets = append(buckets, Bucket{
			PMass: m.p,
			QMass: m.q,
			Ratio: new(big.Rat).Quo(m.p, m.q),
		})
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Ratio.Cmp(buckets[j].Ratio) > 0
	})

	return &Mechanism{
		Finite: buckets,
		Zero: ZeroEvents{
			BothZero: bothZero,
			POnly:    pOnly,
			QOnly:    qOnly,
		},
	}, nil
}

// totalPMass returns the sum of finite-ratio P mass (excluding POnly events).
func (m *Mechanism) finitePMass() *big.Rat {
	total := new(big.Rat)
	for _, b := range m.Finite {
		total.Add(total, b.PMass)
	}
	return total
}

// totalQMass returns the sum of finite-ratio Q mass (excluding QOnly events).
func (m *Mechanism) finiteQMass() *big.Rat {
	total := new(big.Rat)
	for _, b := range m.Finite {
		total.Add(total, b.QMass)
	}
	return total
}
