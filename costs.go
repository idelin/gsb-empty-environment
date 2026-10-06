// Package segconst computes optimal piecewise-constant approximations of
// integer sequences. The objective is the exact sum of squared errors plus
// a fixed rational penalty per segment, with every segment required to
// contain at least m items. Ties are broken by preferring fewer segments,
// then the lexicographically smallest boundary list.
package segconst

import "math/big"

// Costs holds precomputed exact interval costs for one integer sequence.
// A single *Costs value can be reused to solve for many penalty parameters.
type Costs struct {
	n     int
	sum   []*big.Int // prefix sums, len n+1
	sq    []*big.Int // prefix sums of squares, len n+1
	cache map[[2]int]*big.Rat
}

// NewCosts builds the prefix-sum tables for xs in O(n) big-integer work.
func NewCosts(xs []int64) *Costs {
	n := len(xs)
	c := &Costs{
		n:     n,
		sum:   make([]*big.Int, n+1),
		sq:    make([]*big.Int, n+1),
		cache: make(map[[2]int]*big.Rat),
	}
	c.sum[0] = new(big.Int)
	c.sq[0] = new(big.Int)
	for i, x := range xs {
		v := big.NewInt(x)
		c.sum[i+1] = new(big.Int).Add(c.sum[i], v)
		c.sq[i+1] = new(big.Int).Add(c.sq[i], new(big.Int).Mul(v, v))
	}
	return c
}

// Len returns the sequence length.
func (c *Costs) Len() int { return c.n }

// SSE returns the exact sum of squared deviations from the mean over
// the half-open interval [i, j): sum(x^2) - sum(x)^2/len.
func (c *Costs) SSE(i, j int) *big.Rat {
	if i < 0 || j > c.n || i >= j {
		panic("segconst: invalid interval")
	}
	key := [2]int{i, j}
	if v, ok := c.cache[key]; ok {
		return new(big.Rat).Set(v)
	}
	length := int64(j - i)
	s := new(big.Int).Sub(c.sum[j], c.sum[i])
	q := new(big.Int).Sub(c.sq[j], c.sq[i])
	// SSE = (len*q - s^2) / len, always >= 0.
	num := new(big.Int).Mul(big.NewInt(length), q)
	sq := new(big.Int).Mul(s, s)
	num.Sub(num, sq)
	r := new(big.Rat).SetFrac(num, big.NewInt(length))
	c.cache[key] = r
	return new(big.Rat).Set(r)
}

// Mean returns the exact mean over the half-open interval [i, j).
func (c *Costs) Mean(i, j int) *big.Rat {
	if i < 0 || j > c.n || i >= j {
		panic("segconst: invalid interval")
	}
	s := new(big.Int).Sub(c.sum[j], c.sum[i])
	return new(big.Rat).SetFrac(s, big.NewInt(int64(j-i)))
}
