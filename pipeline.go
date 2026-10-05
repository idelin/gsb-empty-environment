package fxacct

import (
	"fmt"
	"math/big"
)

// RoundMode selects how exact ties are broken at a Round node.
type RoundMode int

const (
	RoundHalfAwayFromZero RoundMode = iota
	RoundHalfEven
)

func (m RoundMode) String() string {
	switch m {
	case RoundHalfEven:
		return "half-even"
	default:
		return "half-away-from-zero"
	}
}

// Step is one node of a conversion pipeline. Values between nodes
// are exact rationals; only Round nodes quantize.
type Step interface {
	Name() string
	// Apply transforms m, returning the result and, for rounding
	// nodes, the rounding delta (result minus input). Delta is nil
	// for exact (non-rounding) steps.
	Apply(m Money) (result Money, delta *big.Rat, err error)
	Rounding() bool
}

// Convert multiplies by an exact rational exchange rate. It never
// rounds: the product stays a fraction.
type Convert struct {
	StepName string
	From     string // optional; validated when non-empty
	To       string
	Rate     *big.Rat
}

func (c Convert) Name() string {
	if c.StepName != "" {
		return c.StepName
	}
	return fmt.Sprintf("convert %s->%s @ %s", c.From, c.To, c.Rate.RatString())
}

func (c Convert) Rounding() bool { return false }

func (c Convert) Apply(m Money) (Money, *big.Rat, error) {
	if c.Rate == nil {
		return Money{}, nil, fmt.Errorf("fxacct: step %q has nil rate", c.Name())
	}
	if c.From != "" && m.Currency != c.From {
		return Money{}, nil, fmt.Errorf("fxacct: step %q expects %s, got %s", c.Name(), c.From, m.Currency)
	}
	return Money{Value: new(big.Rat).Mul(m.Value, c.Rate), Currency: c.To}, nil, nil
}

// Round quantizes to 10^-MinorUnit of the currency (MinorUnit 2 is
// cents). This is the only node allowed to lose precision.
type Round struct {
	StepName  string
	MinorUnit int
	Mode      RoundMode
}

func (r Round) Name() string {
	if r.StepName != "" {
		return r.StepName
	}
	return fmt.Sprintf("round to 1e-%d (%s)", r.MinorUnit, r.Mode)
}

func (r Round) Rounding() bool { return true }

func (r Round) Apply(m Money) (Money, *big.Rat, error) {
	if r.MinorUnit < 0 {
		return Money{}, nil, fmt.Errorf("fxacct: step %q has negative minor unit", r.Name())
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(r.MinorUnit)), nil)
	scaleRat := new(big.Rat).SetInt(scale)
	scaled := new(big.Rat).Mul(m.Value, scaleRat)
	rounded := roundToInt(scaled, r.Mode)
	out := new(big.Rat).Quo(rounded, scaleRat)
	delta := new(big.Rat).Sub(out, m.Value)
	return Money{Value: out, Currency: m.Currency}, delta, nil
}

// roundToInt rounds a rational to the nearest integer exactly.
func roundToInt(r *big.Rat, mode RoundMode) *big.Rat {
	num, den := r.Num(), r.Denom()
	q := new(big.Int)
	rem := new(big.Int)
	q.QuoRem(num, den, rem) // truncated toward zero; den > 0
	if rem.Sign() == 0 {
		return new(big.Rat).SetInt(q)
	}
	twice := new(big.Int).Lsh(new(big.Int).Abs(rem), 1)
	switch cmp := twice.Cmp(den); {
	case cmp > 0:
		bump(q, r.Sign())
	case cmp == 0:
		switch mode {
		case RoundHalfAwayFromZero:
			bump(q, r.Sign())
		case RoundHalfEven:
			if q.Bit(0) == 1 { // odd -> round to even
				bump(q, r.Sign())
			}
		}
	}
	return new(big.Rat).SetInt(q)
}

// bump moves q one unit away from zero in the direction of sign.
func bump(q *big.Int, sign int) {
	if sign >= 0 {
		q.Add(q, big.NewInt(1))
	} else {
		q.Sub(q, big.NewInt(1))
	}
}
