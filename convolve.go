package distconv

import (
	"errors"
	"math/big"
	"sort"
)

// Convolve returns the distribution of the sum of two independent random
// variables with distributions a and b.
func Convolve(a, b *Dist) *Dist {
	mass := make(map[int64]*big.Rat, len(a.vals)*len(b.vals))
	tmp := new(big.Rat)
	for i, va := range a.vals {
		for j, vb := range b.vals {
			tmp.Mul(a.probs[i], b.probs[j])
			s := va + vb
			if acc, ok := mass[s]; ok {
				acc.Add(acc, tmp)
			} else {
				mass[s] = new(big.Rat).Set(tmp)
			}
		}
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
	return d
}

// ConvolveAll returns the distribution of the sum of independent variables
// with the given distributions, convolving pairwise in a balanced tree to
// keep intermediate supports small.
func ConvolveAll(dists []*Dist) (*Dist, error) {
	if len(dists) == 0 {
		return nil, errors.New("distconv: no distributions to convolve")
	}
	return convolveRange(dists, 0, len(dists)), nil
}

func convolveRange(dists []*Dist, lo, hi int) *Dist {
	if hi-lo == 1 {
		return dists[lo]
	}
	mid := lo + (hi-lo)/2
	return Convolve(convolveRange(dists, lo, mid), convolveRange(dists, mid, hi))
}
