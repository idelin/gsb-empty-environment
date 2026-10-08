package material

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Vector is a provenance vector: source ID -> share (rational, in [0,1]).
// The shares of a lot's Vector always sum to exactly 1.
type Vector map[string]*big.Rat

// Pure returns the unit vector for a single source.
func Pure(source string) Vector {
	return Vector{source: big.NewRat(1, 1)}
}

// Clone deep-copies the vector.
func (v Vector) Clone() Vector {
	out := make(Vector, len(v))
	for k, x := range v {
		out[k] = new(big.Rat).Set(x)
	}
	return out
}

// Scale returns v * s (element-wise), without mutating v.
func (v Vector) Scale(s *big.Rat) Vector {
	out := make(Vector, len(v))
	for k, x := range v {
		out[k] = new(big.Rat).Mul(x, s)
	}
	return out
}

// Add returns a + b element-wise, without mutating either.
func (v Vector) Add(o Vector) Vector {
	out := v.Clone()
	for k, x := range o {
		if acc, ok := out[k]; ok {
			acc.Add(acc, x)
		} else {
			out[k] = new(big.Rat).Set(x)
		}
	}
	return out
}

// Sum returns the total of all shares.
func (v Vector) Sum() *big.Rat {
	t := new(big.Rat)
	for _, x := range v {
		t.Add(t, x)
	}
	return t
}

// IsNormalized reports whether shares sum to exactly 1.
func (v Vector) IsNormalized() bool {
	return v.Sum().Cmp(big.NewRat(1, 1)) == 0
}

// Equal reports element-wise rational equality.
func (v Vector) Equal(o Vector) bool {
	if len(v) != len(o) {
		return false
	}
	for k, x := range v {
		y, ok := o[k]
		if !ok || x.Cmp(y) != 0 {
			return false
		}
	}
	return true
}

// Weighted merges lots into one provenance vector:
// out = sum(qty_i * vec_i) / sum(qty_i), exact rational arithmetic.
func Weighted(qtys []int64, vecs []Vector) Vector {
	total := new(big.Rat)
	acc := Vector{}
	for i, q := range qtys {
		w := big.NewRat(q, 1)
		total.Add(total, w)
		acc = acc.Add(vecs[i].Scale(w))
	}
	if total.Sign() == 0 {
		return Vector{}
	}
	inv := new(big.Rat).Inv(total)
	return acc.Scale(inv)
}

func (v Vector) String() string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s·%s", v[k].RatString(), k))
	}
	return strings.Join(parts, " + ")
}
