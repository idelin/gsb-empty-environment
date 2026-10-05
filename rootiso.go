// Package rootiso exactly counts and isolates the distinct real roots of an
// integer-coefficient polynomial inside a rational open interval.
//
// Method: the polynomial is first made square-free (multiple roots are
// deduplicated by dividing out gcd(p, p')), then a Sturm sequence over Q[x]
// gives exact root counts for any rational interval, and dyadic-rational
// bisection isolates each root. Every computation uses math/big exact
// rational arithmetic; no fixed-precision (floating-point) approximation is
// ever used as a verdict.
package rootiso

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// IntervalRoot isolates exactly one distinct real root in the open interval
// (Lo, Hi). SignLo and SignHi are the signs of the square-free part of the
// input polynomial at Lo and Hi; they always differ, which is the exact
// sign-change evidence certifying an odd crossing (hence a root) inside.
type IntervalRoot struct {
	Lo, Hi         *big.Rat
	SignLo, SignHi int
}

// Result is the outcome of an isolation run.
type Result struct {
	// Points are exact rational roots reported separately: query endpoints
	// that are roots, and dyadic bisection points that happen to be roots.
	Points []*big.Rat
	// Intervals are pairwise disjoint open intervals, each containing
	// exactly one distinct real root, with sign-change evidence.
	Intervals []IntervalRoot
	// SquareFree is the human-readable square-free part actually used.
	SquareFree string
}

// Count is the total number of distinct real roots found.
func (r *Result) Count() int { return len(r.Points) + len(r.Intervals) }

// ratPoly is a polynomial over Q, coeffs ascending, trimmed (empty == zero).
type ratPoly []*big.Rat

func ratTrim(p ratPoly) ratPoly {
	n := len(p)
	for n > 0 && p[n-1].Sign() == 0 {
		n--
	}
	return p[:n]
}

func ratDeg(p ratPoly) int { return len(p) - 1 }

func intToRatPoly(coeffs []*big.Int) ratPoly {
	p := make(ratPoly, len(coeffs))
	for i, c := range coeffs {
		p[i] = new(big.Rat).SetInt(c)
	}
	return ratTrim(p)
}

func ratDer(p ratPoly) ratPoly {
	if len(p) <= 1 {
		return nil
	}
	d := make(ratPoly, len(p)-1)
	for i := 1; i < len(p); i++ {
		d[i-1] = new(big.Rat).Mul(new(big.Rat).SetInt64(int64(i)), p[i])
	}
	return ratTrim(d)
}

func ratEval(p ratPoly, x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p[i])
	}
	return acc
}

func ratSignAt(p ratPoly, x *big.Rat) int { return ratEval(p, x).Sign() }

func ratNeg(p ratPoly) ratPoly {
	q := make(ratPoly, len(p))
	for i, c := range p {
		q[i] = new(big.Rat).Neg(c)
	}
	return q
}

// ratDivMod divides a by b (b nonzero) over Q[x].
func ratDivMod(a, b ratPoly) (quo, rem ratPoly) {
	if len(b) == 0 {
		panic("rootiso: division by zero polynomial")
	}
	rem = make(ratPoly, len(a))
	for i, c := range a {
		rem[i] = new(big.Rat).Set(c)
	}
	rem = ratTrim(rem)
	if dq := len(rem) - len(b); dq >= 0 {
		quo = make(ratPoly, dq+1)
		for i := range quo {
			quo[i] = new(big.Rat)
		}
	}
	invLC := new(big.Rat).Inv(b[len(b)-1])
	for len(rem) >= len(b) && len(rem) > 0 {
		k := len(rem) - len(b)
		c := new(big.Rat).Mul(rem[len(rem)-1], invLC)
		quo[k] = c
		for i := 0; i < len(b); i++ {
			rem[k+i].Sub(rem[k+i], new(big.Rat).Mul(c, b[i]))
		}
		rem = ratTrim(rem)
	}
	return ratTrim(quo), rem
}

func ratMonic(p ratPoly) ratPoly {
	if len(p) == 0 {
		return p
	}
	inv := new(big.Rat).Inv(p[len(p)-1])
	q := make(ratPoly, len(p))
	for i, c := range p {
		q[i] = new(big.Rat).Mul(c, inv)
	}
	return q
}

// ratGCD is the monic greatest common divisor over Q[x].
func ratGCD(a, b ratPoly) ratPoly {
	for len(b) > 0 {
		_, r := ratDivMod(a, b)
		a, b = b, r
	}
	return ratMonic(a)
}

// squareFreePart returns p / gcd(p, p'): same distinct roots, all simple.
func squareFreePart(coeffs []*big.Int) (ratPoly, error) {
	p := intToRatPoly(coeffs)
	if len(p) == 0 {
		return nil, errors.New("rootiso: zero polynomial rejected (every real number is a root)")
	}
	if ratDeg(p) == 0 {
		return p, nil
	}
	g := ratGCD(p, ratDer(p))
	q, _ := ratDivMod(p, g)
	return q, nil
}

// sturmSequence builds the Sturm sequence of a square-free polynomial.
func sturmSequence(p ratPoly) []ratPoly {
	seq := []ratPoly{p}
	if ratDeg(p) == 0 {
		return seq
	}
	seq = append(seq, ratDer(p))
	for {
		n := len(seq)
		_, r := ratDivMod(seq[n-2], seq[n-1])
		r = ratTrim(ratNeg(r))
		if len(r) == 0 {
			break
		}
		seq = append(seq, r)
	}
	return seq
}

// signVariations counts sign changes at x, skipping zeros. With this
// convention, for a Sturm sequence S and any a < b (roots allowed),
// V(S,a) - V(S,b) equals the number of distinct roots in (a, b].
func signVariations(seq []ratPoly, x *big.Rat) int {
	prev, v := 0, 0
	for _, s := range seq {
		sg := ratSignAt(s, x)
		if sg == 0 {
			continue
		}
		if prev != 0 && sg != prev {
			v++
		}
		prev = sg
	}
	return v
}

type isolator struct {
	sturm []ratPoly
	sq    ratPoly // square-free part
	res   *Result
}

// countOpen returns the exact number of distinct roots in the open interval
// (a, b): roots in (a,b] minus a possible root at b.
func (is *isolator) countOpen(a, b *big.Rat) int {
	if ratDeg(is.sq) <= 0 {
		return 0
	}
	n := signVariations(is.sturm, a) - signVariations(is.sturm, b)
	if ratSignAt(is.sq, b) == 0 {
		n--
	}
	return n
}

func midpoint(a, b *big.Rat) *big.Rat {
	m := new(big.Rat).Add(a, b)
	return m.Quo(m, big.NewRat(2, 1))
}

// isolate emits all roots in the open interval (lo, hi), given that exactly
// n of them lie there. Bisection only ever consults exact Sturm counts.
func (is *isolator) isolate(lo, hi *big.Rat, n int) {
	if n == 0 {
		return
	}
	if n == 1 && ratSignAt(is.sq, lo) != 0 && ratSignAt(is.sq, hi) != 0 {
		is.res.Intervals = append(is.res.Intervals, IntervalRoot{
			Lo:     new(big.Rat).Set(lo),
			Hi:     new(big.Rat).Set(hi),
			SignLo: ratSignAt(is.sq, lo),
			SignHi: ratSignAt(is.sq, hi),
		})
		return
	}
	mid := midpoint(lo, hi)
	if ratSignAt(is.sq, mid) == 0 {
		// Exact rational root hit by bisection: report it as a point and
		// keep isolating strictly to its left and right.
		is.res.Points = append(is.res.Points, mid)
		is.isolate(lo, mid, is.countOpen(lo, mid))
		is.isolate(mid, hi, is.countOpen(mid, hi))
		return
	}
	nL := is.countOpen(lo, mid)
	is.isolate(lo, mid, nL)
	is.isolate(mid, hi, n-nL)
}

// Isolate counts and isolates the distinct real roots of the polynomial with
// integer coefficients coeffs (coeffs[i] multiplies x^i) inside the rational
// open interval (lo, hi). Roots equal to lo or hi are reported separately as
// exact points. Multiple roots are deduplicated via square-free
// factorization. The zero polynomial is rejected.
func Isolate(coeffs []*big.Int, lo, hi *big.Rat) (*Result, error) {
	if lo.Cmp(hi) >= 0 {
		return nil, fmt.Errorf("rootiso: invalid interval (%s, %s): need lo < hi", lo, hi)
	}
	sq, err := squareFreePart(coeffs)
	if err != nil {
		return nil, err
	}
	is := &isolator{sq: sq, res: &Result{}}
	if ratDeg(sq) > 0 {
		is.sturm = sturmSequence(sq)
	}
	if ratSignAt(sq, lo) == 0 {
		is.res.Points = append(is.res.Points, new(big.Rat).Set(lo))
	}
	if ratSignAt(sq, hi) == 0 {
		is.res.Points = append(is.res.Points, new(big.Rat).Set(hi))
	}
	is.isolate(lo, hi, is.countOpen(lo, hi))
	is.res.SquareFree = ratPolyString(sq)
	is.res.sort()
	return is.res, nil
}

// IsolateAll isolates every distinct real root of the polynomial, using a
// Cauchy bound as the search interval.
func IsolateAll(coeffs []*big.Int) (*Result, error) {
	n := len(coeffs)
	for n > 0 && coeffs[n-1].Sign() == 0 {
		n--
	}
	if n == 0 {
		return nil, errors.New("rootiso: zero polynomial rejected (every real number is a root)")
	}
	lead := new(big.Rat).SetInt(new(big.Int).Abs(coeffs[n-1]))
	maxRatio := new(big.Rat)
	for i := 0; i < n-1; i++ {
		r := new(big.Rat).SetInt(new(big.Int).Abs(coeffs[i]))
		r.Quo(r, lead)
		if r.Cmp(maxRatio) > 0 {
			maxRatio = r
		}
	}
	bound := new(big.Rat).Add(maxRatio, big.NewRat(1, 1))
	return Isolate(coeffs, new(big.Rat).Neg(bound), bound)
}

func (r *Result) sort() {
	sort.Slice(r.Points, func(i, j int) bool { return r.Points[i].Cmp(r.Points[j]) < 0 })
	sort.Slice(r.Intervals, func(i, j int) bool { return r.Intervals[i].Lo.Cmp(r.Intervals[j].Lo) < 0 })
}

func ratPolyString(p ratPoly) string {
	if len(p) == 0 {
		return "0"
	}
	one := big.NewRat(1, 1)
	var b strings.Builder
	first := true
	for i := len(p) - 1; i >= 0; i-- {
		c := p[i]
		if c.Sign() == 0 {
			continue
		}
		sign := "+"
		if c.Sign() < 0 {
			sign = "-"
		}
		abs := new(big.Rat).Abs(c)
		isOne := abs.Cmp(one) == 0
		var term string
		switch {
		case i == 0:
			term = abs.RatString()
		case i == 1:
			if isOne {
				term = "x"
			} else {
				term = abs.RatString() + "*x"
			}
		default:
			if isOne {
				term = fmt.Sprintf("x^%d", i)
			} else {
				term = fmt.Sprintf("%s*x^%d", abs.RatString(), i)
			}
		}
		if first {
			if sign == "-" {
				b.WriteString("- ")
			}
			b.WriteString(term)
			first = false
		} else {
			fmt.Fprintf(&b, " %s %s", sign, term)
		}
	}
	return b.String()
}
