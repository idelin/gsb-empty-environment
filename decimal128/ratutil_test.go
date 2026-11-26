package decimal128

import (
	"math/big"
	"math/rand"
	"strconv"
	"strings"
)

// parseRat mirrors the accepted literal grammar (fixed or scientific) using
// big.Rat as the oracle. It lives in the test-only files, so production code
// never depends on math/big.
func parseRat(s string) *big.Rat {
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	mantissa := s
	exp := 0
	if e := strings.IndexAny(s, "eE"); e >= 0 {
		mantissa = s[:e]
		exp, _ = strconv.Atoi(s[e+1:])
	}
	frac := ""
	intp := mantissa
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		intp, frac = mantissa[:dot], mantissa[dot+1:]
	}
	digits := intp + frac
	n, _ := new(big.Int).SetString(digits, 10)
	r := new(big.Rat).SetInt(n)
	if len(frac) > 0 {
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(frac))), nil)
		r.Quo(r, new(big.Rat).SetInt(pow))
	}
	if exp != 0 {
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(exp))), nil)
		if exp > 0 {
			r.Mul(r, new(big.Rat).SetInt(factor))
		} else {
			r.Quo(r, new(big.Rat).SetInt(factor))
		}
	}
	if neg {
		r.Neg(r)
	}
	return r
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ratRound returns r rounded to scale fractional digits using mode, as the
// integer coefficient of the fixed-point representation.
func ratRound(r *big.Rat, scale int, mode RoundingMode) *big.Int {
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	num := new(big.Int).Mul(r.Num(), pow)
	q, rem := new(big.Int).QuoRem(num, r.Denom(), new(big.Int))
	// big.Int.QuoRem truncates toward zero; rem shares the sign of num.
	if rem.Sign() == 0 {
		return q
	}
	den := new(big.Int).Abs(r.Denom())
	twice := new(big.Int).Abs(rem)
	twice.Mul(twice, big.NewInt(2))
	cmp := twice.Cmp(den)
	up := false
	switch mode {
	case HalfAwayFromZero:
		up = cmp >= 0
	case HalfToEven:
		if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
			up = true
		}
	case Truncate:
		up = false
	}
	if up {
		if q.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	return q
}

// ratFixed renders r at exactly scale fractional digits.
func ratFixed(r *big.Rat, scale int) string {
	c := ratRound(r, scale, HalfAwayFromZero)
	neg := c.Sign() < 0
	abs := new(big.Int).Abs(c).String()
	if scale == 0 {
		if neg {
			return "-" + abs
		}
		return abs
	}
	if len(abs) <= scale {
		abs = strings.Repeat("0", scale-len(abs)+1) + abs
	}
	out := abs[:len(abs)-scale] + "." + abs[len(abs)-scale:]
	if neg {
		return "-" + out
	}
	return out
}

// randomDecimal builds a random literal with up to 38 significant digits and a
// scale in [0, 12], plus its exact rational value.
func randomDecimal(rng *rand.Rand) (string, *big.Rat) {
	n := 1 + rng.Intn(38)
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i == 0 {
			b.WriteByte(byte('1' + rng.Intn(9)))
		} else {
			b.WriteByte(byte('0' + rng.Intn(10)))
		}
	}
	digits := b.String()
	scale := rng.Intn(n + 1)
	if scale > 12 {
		scale = 12
	}
	literal := digits
	if scale > 0 {
		literal = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if rng.Intn(2) == 0 {
		literal = "-" + literal
	}
	return literal, parseRat(literal)
}
