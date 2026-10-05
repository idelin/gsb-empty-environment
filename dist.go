// Package distconv computes exact distributions of sums of independent
// discrete random variables with finite integer support and rational
// probabilities, together with quantiles and tail probabilities.
package distconv

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Pair associates an integer value with its probability mass.
type Pair struct {
	Value int64
	Prob  *big.Rat
}

// Dist is a probability distribution over a finite set of int64 values
// with exact rational probabilities. Values are stored sorted and unique,
// zero-mass entries are removed, and the total mass is exactly 1.
type Dist struct {
	vals  []int64
	probs []*big.Rat
	cum   []*big.Rat // cum[i] = P(X <= vals[i])
}

var errEmpty = errors.New("distconv: distribution has no positive-mass values")

// New builds a Dist from (value, probability) pairs. Duplicate values are
// merged by summing their probabilities, zero-probability entries are
// removed, negative probabilities are rejected, and the merged
// probabilities must sum to exactly 1.
func New(pairs []Pair) (*Dist, error) {
	mass := make(map[int64]*big.Rat, len(pairs))
	for _, p := range pairs {
		if p.Prob == nil {
			return nil, errors.New("distconv: nil probability")
		}
		if p.Prob.Sign() < 0 {
			return nil, fmt.Errorf("distconv: negative probability %v for value %d", p.Prob, p.Value)
		}
		if p.Prob.Sign() == 0 {
			continue
		}
		if acc, ok := mass[p.Value]; ok {
			acc.Add(acc, p.Prob)
		} else {
			mass[p.Value] = new(big.Rat).Set(p.Prob)
		}
	}
	if len(mass) == 0 {
		return nil, errEmpty
	}

	vals := make([]int64, 0, len(mass))
	for v := range mass {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })

	d := &Dist{
		vals:  vals,
		probs: make([]*big.Rat, len(vals)),
		cum:   make([]*big.Rat, len(vals)),
	}
	total := new(big.Rat)
	for i, v := range vals {
		d.probs[i] = mass[v]
		total.Add(total, mass[v])
		d.cum[i] = new(big.Rat).Set(total)
	}
	if total.Cmp(big.NewRat(1, 1)) != 0 {
		return nil, fmt.Errorf("distconv: probabilities sum to %v, not 1", total)
	}
	return d, nil
}

// Must is New but panics on error; convenient for literals in tests.
func Must(pairs []Pair) *Dist {
	d, err := New(pairs)
	if err != nil {
		panic(err)
	}
	return d
}

// Degenerate returns the distribution placing all mass on v.
func Degenerate(v int64) *Dist {
	return &Dist{
		vals:  []int64{v},
		probs: []*big.Rat{big.NewRat(1, 1)},
		cum:   []*big.Rat{big.NewRat(1, 1)},
	}
}

// Size returns the number of distinct values with positive mass.
func (d *Dist) Size() int { return len(d.vals) }

// Min and Max return the smallest and largest values with positive mass.
func (d *Dist) Min() int64 { return d.vals[0] }
func (d *Dist) Max() int64 { return d.vals[len(d.vals)-1] }

// Values returns a copy of the sorted support values.
func (d *Dist) Values() []int64 {
	out := make([]int64, len(d.vals))
	copy(out, d.vals)
	return out
}

// Prob returns P(X = v), zero if v is outside the support.
func (d *Dist) Prob(v int64) *big.Rat {
	i := sort.Search(len(d.vals), func(i int) bool { return d.vals[i] >= v })
	if i < len(d.vals) && d.vals[i] == v {
		return new(big.Rat).Set(d.probs[i])
	}
	return new(big.Rat)
}

// CDF returns P(X <= t).
func (d *Dist) CDF(t int64) *big.Rat {
	i := sort.Search(len(d.vals), func(i int) bool { return d.vals[i] > t })
	if i == 0 {
		return new(big.Rat)
	}
	return new(big.Rat).Set(d.cum[i-1])
}

// SF returns P(X > t).
func (d *Dist) SF(t int64) *big.Rat {
	return new(big.Rat).Sub(big.NewRat(1, 1), d.CDF(t))
}

// Less returns P(X < t).
func (d *Dist) Less(t int64) *big.Rat {
	i := sort.Search(len(d.vals), func(i int) bool { return d.vals[i] >= t })
	if i == 0 {
		return new(big.Rat)
	}
	return new(big.Rat).Set(d.cum[i-1])
}

// Geq returns P(X >= t).
func (d *Dist) Geq(t int64) *big.Rat {
	return new(big.Rat).Sub(big.NewRat(1, 1), d.Less(t))
}

// Quantile returns the smallest value v with P(X <= v) >= p.
// p must satisfy 0 < p <= 1. When p falls exactly on a cumulative
// boundary, the value at that boundary is returned.
func (d *Dist) Quantile(p *big.Rat) (int64, error) {
	if p == nil {
		return 0, errors.New("distconv: nil quantile probability")
	}
	if p.Sign() <= 0 || p.Cmp(big.NewRat(1, 1)) > 0 {
		return 0, fmt.Errorf("distconv: quantile probability %v outside (0, 1]", p)
	}
	i := sort.Search(len(d.cum), func(i int) bool { return d.cum[i].Cmp(p) >= 0 })
	return d.vals[i], nil
}

// Mean returns the exact expected value.
func (d *Dist) Mean() *big.Rat {
	m := new(big.Rat)
	tmp := new(big.Rat)
	for i, v := range d.vals {
		tmp.SetInt64(v)
		tmp.Mul(tmp, d.probs[i])
		m.Add(m, tmp)
	}
	return m
}

// String renders the distribution as sorted value:probability pairs.
func (d *Dist) String() string {
	s := "{"
	for i, v := range d.vals {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d:%v", v, d.probs[i])
	}
	return s + "}"
}
