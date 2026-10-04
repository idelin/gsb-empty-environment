// Package conv computes exact distributions of sums of independent
// integer-valued random variables with rational probabilities.
package conv

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Entry is a single (value, probability) atom of a distribution.
type Entry struct {
	Val  int64
	Prob *big.Rat
}

// Dist is a discrete probability distribution over a finite set of int64
// values. Entries are sorted by value, probabilities are positive and sum
// to exactly 1.
type Dist struct {
	entries []Entry
	cum     []*big.Rat // cum[i] = P(X <= entries[i].Val)
}

var one = big.NewRat(1, 1)

// NewDist builds a distribution from parallel slices of values and
// probabilities. Duplicate values are merged, zero probabilities are
// dropped, and the remaining probabilities must sum to exactly 1.
func NewDist(vals []int64, probs []*big.Rat) (*Dist, error) {
	if len(vals) != len(probs) {
		return nil, errors.New("conv: vals and probs length mismatch")
	}
	if len(vals) == 0 {
		return nil, errors.New("conv: empty distribution")
	}
	acc := make(map[int64]*big.Rat, len(vals))
	for i, v := range vals {
		p := probs[i]
		if p == nil {
			return nil, fmt.Errorf("conv: nil probability at index %d", i)
		}
		if p.Sign() < 0 {
			return nil, fmt.Errorf("conv: negative probability at index %d", i)
		}
		if p.Sign() == 0 {
			continue
		}
		if s, ok := acc[v]; ok {
			s.Add(s, p)
		} else {
			acc[v] = new(big.Rat).Set(p)
		}
	}
	return distFromMap(acc)
}

// Degenerate returns the distribution that takes value v with probability 1.
func Degenerate(v int64) *Dist {
	return &Dist{
		entries: []Entry{{Val: v, Prob: big.NewRat(1, 1)}},
		cum:     []*big.Rat{big.NewRat(1, 1)},
	}
}

func distFromMap(acc map[int64]*big.Rat) (*Dist, error) {
	if len(acc) == 0 {
		return nil, errors.New("conv: all probabilities are zero")
	}
	entries := make([]Entry, 0, len(acc))
	total := new(big.Rat)
	for v, p := range acc {
		total.Add(total, p)
		entries = append(entries, Entry{Val: v, Prob: p})
	}
	if total.Cmp(one) != 0 {
		return nil, fmt.Errorf("conv: probabilities sum to %s, not 1", total.RatString())
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Val < entries[j].Val })
	return finalize(entries), nil
}

func finalize(entries []Entry) *Dist {
	cum := make([]*big.Rat, len(entries))
	run := new(big.Rat)
	for i, e := range entries {
		run.Add(run, e.Prob)
		cum[i] = new(big.Rat).Set(run)
	}
	return &Dist{entries: entries, cum: cum}
}

// Entries returns the (value, probability) atoms in increasing value order.
func (d *Dist) Entries() []Entry {
	out := make([]Entry, len(d.entries))
	copy(out, d.entries)
	return out
}

// Prob returns P(X = v), zero if v is not in the support.
func (d *Dist) Prob(v int64) *big.Rat {
	i := sort.Search(len(d.entries), func(i int) bool { return d.entries[i].Val >= v })
	if i < len(d.entries) && d.entries[i].Val == v {
		return new(big.Rat).Set(d.entries[i].Prob)
	}
	return new(big.Rat)
}

// ProbLE returns P(X <= v).
func (d *Dist) ProbLE(v int64) *big.Rat {
	i := sort.Search(len(d.entries), func(i int) bool { return d.entries[i].Val > v })
	if i == 0 {
		return new(big.Rat)
	}
	return new(big.Rat).Set(d.cum[i-1])
}

// ProbLT returns P(X < v).
func (d *Dist) ProbLT(v int64) *big.Rat { return d.ProbLE(v - 1) }

// ProbGE returns P(X >= v).
func (d *Dist) ProbGE(v int64) *big.Rat {
	return new(big.Rat).Sub(one, d.ProbLE(v-1))
}

// ProbGT returns P(X > v).
func (d *Dist) ProbGT(v int64) *big.Rat {
	return new(big.Rat).Sub(one, d.ProbLE(v))
}

// Quantile returns the smallest value v with P(X <= v) >= p.
// p must satisfy 0 < p <= 1.
func (d *Dist) Quantile(p *big.Rat) (int64, error) {
	if p == nil || p.Sign() <= 0 || p.Cmp(one) > 0 {
		return 0, fmt.Errorf("conv: quantile probability %v out of range (0, 1]", p)
	}
	i := sort.Search(len(d.cum), func(i int) bool { return d.cum[i].Cmp(p) >= 0 })
	return d.entries[i].Val, nil
}

// Convolve returns the distribution of X + Y for independent X ~ a, Y ~ b.
func Convolve(a, b *Dist) *Dist {
	acc := make(map[int64]*big.Rat, len(a.entries)*len(b.entries))
	for _, ea := range a.entries {
		for _, eb := range b.entries {
			v := ea.Val + eb.Val
			p := new(big.Rat).Mul(ea.Prob, eb.Prob)
			if s, ok := acc[v]; ok {
				s.Add(s, p)
			} else {
				acc[v] = p
			}
		}
	}
	entries := make([]Entry, 0, len(acc))
	for v, p := range acc {
		entries = append(entries, Entry{Val: v, Prob: p})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Val < entries[j].Val })
	return finalize(entries)
}
