// Package taxcalc implements a self-contained tax-calculation toy model.
// Tax brackets and deduction rules are supplied by the caller, all amounts
// are integer cents, and rounding happens at the end of every bracket.
// Nothing here reflects real tax law.
//
// Model
//
// A gross amount x (integer cents, x >= 0) is taxed bracket by bracket.
// Brackets are contiguous half-open intervals [Lower, Upper) starting at 0;
// the last bracket must be unbounded (Upper == Inf). Each bracket
// contributes round(rate_i * portion_i) to the cumulative tax, where
// portion_i is the part of x falling inside bracket i and the rounding is
// applied per bracket, at the bracket's end. The deduction of the bracket
// containing x (a "quick deduction") is then subtracted and the total tax
// is floored at zero:
//
//	tax(x) = max(0, sum_i round(rate_i * portion_i(x)) - deduction(x))
//	net(x) = x - tax(x)
//
// Because rates and deductions are arbitrary inputs, net(x) is NOT
// necessarily strictly increasing, or even monotone: rounding plateaus,
// zero-tax segments, 100% brackets and deduction cliffs can all make it
// flat, decreasing, or discontinuous. InverseNet accounts for all of that
// and returns every gross interval mapping to a target net amount.
package taxcalc

import (
	"errors"
	"math"
	"math/big"
)

// Inf marks an unbounded bracket upper bound. It also appears as the Hi
// bound of an unbounded Interval returned by InverseNet.
const Inf = int64(math.MaxInt64)

// Rate is an exact rational tax rate, Num/Den (e.g. Rate{1, 5} is 20%).
// Rates of 0 and rates above 100% are both legal.
type Rate struct {
	Num int64
	Den int64
}

// RatePercent returns a rate of p percent.
func RatePercent(p int64) Rate { return Rate{Num: p, Den: 100} }

// RoundMode selects how each bracket's tax is rounded to integer cents.
type RoundMode int

const (
	// RoundHalfUp rounds halves away from zero (the default zero value).
	RoundHalfUp RoundMode = iota
	// RoundDown rounds toward zero (floor for non-negative values).
	RoundDown
	// RoundUp rounds away from zero (ceil for non-negative values).
	RoundUp
)

// Bracket is one tax bracket over the half-open gross range [Lower, Upper).
type Bracket struct {
	Lower int64 // inclusive lower bound, cents
	Upper int64 // exclusive upper bound, cents; Inf for the open-ended top bracket
	Rate  Rate  // marginal rate inside this bracket
	// Deduction is the "quick deduction" (cents) subtracted from the
	// cumulative tax while the gross amount falls inside this bracket.
	// Arbitrary values are allowed, so the net amount may jump at bracket
	// boundaries.
	Deduction int64
}

// Rules is a complete tax schedule.
type Rules struct {
	Brackets []Bracket
	Mode     RoundMode
}

// Validate checks that the schedule is well formed.
func (r Rules) Validate() error {
	if len(r.Brackets) == 0 {
		return errors.New("taxcalc: no brackets")
	}
	if r.Mode != RoundHalfUp && r.Mode != RoundDown && r.Mode != RoundUp {
		return errors.New("taxcalc: invalid rounding mode")
	}
	if r.Brackets[0].Lower != 0 {
		return errors.New("taxcalc: first bracket must start at 0")
	}
	for i, b := range r.Brackets {
		if b.Rate.Den <= 0 {
			return errors.New("taxcalc: rate denominator must be positive")
		}
		if b.Rate.Num < 0 {
			return errors.New("taxcalc: rate must be non-negative")
		}
		last := i == len(r.Brackets)-1
		if b.Upper == Inf {
			if !last {
				return errors.New("taxcalc: only the last bracket may be unbounded")
			}
			continue
		}
		if b.Upper <= b.Lower {
			return errors.New("taxcalc: bracket upper bound must exceed lower bound")
		}
		if last {
			return errors.New("taxcalc: last bracket must be unbounded (Upper = Inf)")
		}
		if r.Brackets[i+1].Lower != b.Upper {
			return errors.New("taxcalc: brackets must be contiguous (gap or overlap)")
		}
	}
	return nil
}

// Tax returns the total tax in cents for a gross amount. The rules must be
// valid (see Validate) and gross must be >= 0.
func (r Rules) Tax(gross int64) int64 {
	if gross < 0 {
		return 0
	}
	cum := big.NewInt(0)
	var ded int64
	for _, b := range r.Brackets {
		if gross < b.Lower {
			break
		}
		ded = b.Deduction
		portion := gross - b.Lower
		if b.Upper != Inf && gross >= b.Upper {
			portion = b.Upper - b.Lower
		}
		cum.Add(cum, roundRate(b.Rate, big.NewInt(portion), r.Mode))
		if b.Upper == Inf || gross < b.Upper {
			break
		}
	}
	tax := new(big.Int).Sub(cum, big.NewInt(ded))
	if tax.Sign() < 0 {
		tax.SetInt64(0)
	}
	return tax.Int64()
}

// Net returns the after-tax amount in cents for a gross amount.
func (r Rules) Net(gross int64) int64 {
	return gross - r.Tax(gross)
}

// roundRate computes round(rate * amount) exactly, using big.Rat
// internally so no precision is lost before rounding.
func roundRate(rate Rate, amount *big.Int, mode RoundMode) *big.Int {
	num := new(big.Int).Mul(big.NewInt(rate.Num), amount)
	v := new(big.Rat).SetFrac(num, big.NewInt(rate.Den))
	return roundRat(v, mode)
}

func roundRat(v *big.Rat, mode RoundMode) *big.Int {
	switch mode {
	case RoundDown:
		return floorRat(v)
	case RoundUp:
		return ceilRat(v)
	default: // RoundHalfUp
		return floorRat(new(big.Rat).Add(v, big.NewRat(1, 2)))
	}
}

func floorRat(v *big.Rat) *big.Int {
	// big.Int.Div is Euclidean, i.e. floor division for a positive divisor.
	return new(big.Int).Div(v.Num(), v.Denom())
}

func ceilRat(v *big.Rat) *big.Int {
	f := floorRat(new(big.Rat).Neg(v))
	return f.Neg(f)
}
