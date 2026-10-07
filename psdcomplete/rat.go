package psdcomplete

import (
	"fmt"
	"math/big"
	"sort"
)

// Rat returns an exact rational with the given integer value.
func Rat(v int64) *big.Rat {
	return big.NewRat(v, 1)
}

// Frac returns an exact rational num/den. It panics if den == 0.
func Frac(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

// ParseRat parses an exact rational from a string such as "3/4", "-2" or
// "0.125". The value is stored exactly.
func ParseRat(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("psdcomplete: invalid rational %q", s)
	}
	return r, nil
}

func cloneRat(r *big.Rat) *big.Rat {
	return new(big.Rat).Set(r)
}

// sortedUniqueRats returns a sorted copy of vals with duplicates removed.
func sortedUniqueRats(vals []*big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(vals))
	for i, v := range vals {
		out[i] = cloneRat(v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cmp(out[j]) < 0 })
	uniq := out[:0]
	for _, v := range out {
		if len(uniq) == 0 || uniq[len(uniq)-1].Cmp(v) != 0 {
			uniq = append(uniq, v)
		}
	}
	return uniq
}

func ratString(r *big.Rat) string {
	if r == nil {
		return "<nil>"
	}
	return r.RatString()
}
