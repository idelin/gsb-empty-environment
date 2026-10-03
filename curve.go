package ratbezier

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Curve is a one-dimensional rational Bezier curve: control point values
// Points with matching positive Weights, evaluated as
//
//	B(t) = sum(w_i * p_i * B_i^n(t)) / sum(w_i * B_i^n(t)),  t in [0, 1]
//
// where B_i^n are the Bernstein basis polynomials.
type Curve struct {
	Points  []*big.Rat
	Weights []*big.Rat
}

// NewCurve builds a curve from float64 inputs. Every float64 is converted
// to its exact rational value, so the analysis remains exact.
func NewCurve(points, weights []float64) (*Curve, error) {
	if len(points) != len(weights) {
		return nil, errors.New("ratbezier: points and weights must have the same length")
	}
	pr := make([]*big.Rat, len(points))
	wr := make([]*big.Rat, len(weights))
	for i, v := range points {
		pr[i] = new(big.Rat).SetFloat64(v)
		if pr[i] == nil {
			return nil, fmt.Errorf("ratbezier: point %d (%v) is not a finite number", i, v)
		}
	}
	for i, v := range weights {
		wr[i] = new(big.Rat).SetFloat64(v)
		if wr[i] == nil {
			return nil, fmt.Errorf("ratbezier: weight %d (%v) is not a finite number", i, v)
		}
	}
	return NewCurveRat(pr, wr)
}

// NewCurveRat builds a curve from exact rational inputs. Weights must be
// strictly positive so the denominator never vanishes on [0, 1].
func NewCurveRat(points, weights []*big.Rat) (*Curve, error) {
	if len(points) != len(weights) {
		return nil, errors.New("ratbezier: points and weights must have the same length")
	}
	if len(points) < 2 {
		return nil, errors.New("ratbezier: need at least 2 control points")
	}
	c := &Curve{
		Points:  make([]*big.Rat, len(points)),
		Weights: make([]*big.Rat, len(weights)),
	}
	for i := range points {
		if points[i] == nil || weights[i] == nil {
			return nil, errors.New("ratbezier: nil point or weight")
		}
		if weights[i].Sign() <= 0 {
			return nil, fmt.Errorf("ratbezier: weight %d must be positive, got %s", i, weights[i].RatString())
		}
		c.Points[i] = new(big.Rat).Set(points[i])
		c.Weights[i] = new(big.Rat).Set(weights[i])
	}
	return c, nil
}

// bernstein returns B_i^n(t) = C(n,i) t^i (1-t)^(n-i) as an integer Poly.
func bernstein(n, i int) *Poly {
	binom := new(big.Int).Binomial(int64(n), int64(i))
	// pw = (1 - t)^(n-i)
	pw := NewPoly(1)
	base := NewPoly(1, -1)
	for k := 0; k < n-i; k++ {
		pw = pw.Mul(base)
	}
	c := make([]*big.Int, i+len(pw.c))
	for j := 0; j < i; j++ {
		c[j] = new(big.Int)
	}
	for j, cf := range pw.c {
		c[i+j] = new(big.Int).Mul(binom, cf)
	}
	return polyFromBig(c)
}

// DerivativePolynomial derives the exact integer-coefficient polynomial
// whose roots in [0, 1] are precisely the critical parameters of the
// curve. Writing the curve as B(t) = P(t)/W(t), the derivative's
// numerator is N(t) = P'(t)W(t) - P(t)W'(t); denominators are cleared
// and the result is made primitive. The denominator W(t)^2 is strictly
// positive on [0, 1] for positive weights, so sign(N) = sign(B').
func (c *Curve) DerivativePolynomial() *Poly {
	n := len(c.Points) - 1
	pSum := &ratPoly{}
	wSum := &ratPoly{}
	for i := 0; i <= n; i++ {
		b := ratPolyFromPoly(bernstein(n, i))
		wp := new(big.Rat).Mul(c.Weights[i], c.Points[i])
		pSum = pSum.add(b.scale(wp))
		wSum = wSum.add(b.scale(c.Weights[i]))
	}
	num := pSum.deriv().mul(wSum).sub(pSum.mul(wSum.deriv()))
	return num.toPoly()
}

// Eval evaluates the curve exactly at a rational parameter t.
func (c *Curve) Eval(t *big.Rat) *big.Rat {
	n := len(c.Points) - 1
	num := new(big.Rat)
	den := new(big.Rat)
	for i := 0; i <= n; i++ {
		b := ratPolyFromPoly(bernstein(n, i)).eval(t)
		num.Add(num, new(big.Rat).Mul(new(big.Rat).Mul(c.Weights[i], c.Points[i]), b))
		den.Add(den, new(big.Rat).Mul(c.Weights[i], b))
	}
	return new(big.Rat).Quo(num, den)
}

// Param locates a critical parameter in [0, 1]. When Exact is non-nil the
// parameter is an exact rational number; otherwise it is an irrational
// root isolated in the closed interval [Lo, Hi].
type Param struct {
	Exact  *big.Rat
	Lo, Hi *big.Rat
}

// IsExact reports whether the parameter is exactly rational.
func (p Param) IsExact() bool { return p.Exact != nil }

// Approx returns the exact value, or the midpoint of the isolating
// interval for irrational roots.
func (p Param) Approx() *big.Rat {
	if p.Exact != nil {
		return new(big.Rat).Set(p.Exact)
	}
	return new(big.Rat).Quo(new(big.Rat).Add(p.Lo, p.Hi), big.NewRat(2, 1))
}

func (p Param) upper() *big.Rat {
	if p.Exact != nil {
		return p.Exact
	}
	return p.Hi
}

func (p Param) lower() *big.Rat {
	if p.Exact != nil {
		return p.Exact
	}
	return p.Lo
}

func (p Param) String() string {
	if p.Exact != nil {
		return p.Exact.RatString()
	}
	return fmt.Sprintf("[%s, %s] (approx %s)", ratDecimal(p.Lo), ratDecimal(p.Hi), ratDecimal(p.Approx()))
}

// ExtremumKind classifies a candidate extremum.
type ExtremumKind int

const (
	// Endpoint is a boundary of the parameter interval [0, 1].
	Endpoint ExtremumKind = iota
	// Maximum is an interior local maximum (derivative + to -).
	Maximum
	// Minimum is an interior local minimum (derivative - to +).
	Minimum
	// Stationary is an interior critical point where the derivative
	// touches zero without changing sign.
	Stationary
)

func (k ExtremumKind) String() string {
	switch k {
	case Endpoint:
		return "endpoint"
	case Maximum:
		return "maximum"
	case Minimum:
		return "minimum"
	default:
		return "stationary"
	}
}

// Extremum is a candidate extremum: a critical point or an endpoint,
// together with the curve value there.
type Extremum struct {
	T     Param
	Value *big.Rat
	Kind  ExtremumKind
}

// MonotoneInterval is a maximal parameter range on which the curve is
// monotonically increasing or decreasing.
type MonotoneInterval struct {
	From, To   Param
	Increasing bool
}

// Result is the full extremum analysis of a curve on [0, 1].
type Result struct {
	// Derivative is the primitive integer polynomial whose roots in
	// [0, 1] are the critical parameters (numerator of B'(t)).
	Derivative *Poly
	// Constant is true when the curve is constant on [0, 1].
	Constant bool
	// CriticalPoints are the interior critical parameters, sorted.
	CriticalPoints []Param
	// Candidates lists endpoints and critical points in t-order with
	// their values; the boundary endpoints 0 and 1 are always included.
	Candidates []Extremum
	// Max and Min are the global maximum and minimum on [0, 1].
	Max, Min Extremum
	// Monotonicity partitions [0, 1] into monotone intervals.
	Monotonicity []MonotoneInterval
}

// DefaultPrecision is the default width of isolating intervals for
// irrational critical points: 1e-30.
func DefaultPrecision() *big.Rat {
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	return new(big.Rat).SetFrac(big.NewInt(1), den)
}

// Analyze locates every critical point of the curve in [0, 1], classifies
// monotone regions, and finds the global extrema. eps bounds the width of
// isolating intervals for irrational critical parameters; pass nil for
// DefaultPrecision. All arithmetic is exact (big.Rat / big.Int).
func (c *Curve) Analyze(eps *big.Rat) *Result {
	if eps == nil || eps.Sign() <= 0 {
		eps = DefaultPrecision()
	}
	d := c.DerivativePolynomial()
	res := &Result{Derivative: d}

	zero := big.NewRat(0, 1)
	one := big.NewRat(1, 1)

	if d.IsZero() {
		res.Constant = true
		v := c.Eval(zero)
		res.Candidates = []Extremum{
			{T: Param{Exact: zero}, Value: v, Kind: Endpoint},
			{T: Param{Exact: one}, Value: v, Kind: Endpoint},
		}
		res.Max = res.Candidates[0]
		res.Min = res.Candidates[0]
		return res
	}

	// Exact rational roots (rational root theorem), then isolate the
	// remaining irrational roots with Sturm sequences + bisection.
	ratRoots, rest := extractRationalRoots(d)
	seen := make(map[string]bool)
	var crits []Param
	for _, r := range ratRoots {
		if r.Sign() <= 0 || r.Cmp(one) >= 0 {
			continue // endpoints are handled separately
		}
		key := r.RatString()
		if seen[key] {
			continue
		}
		seen[key] = true
		crits = append(crits, Param{Exact: new(big.Rat).Set(r)})
	}
	for _, iv := range isolateRoots(rest, zero, one, eps) {
		crits = append(crits, Param{Lo: iv.lo, Hi: iv.hi})
	}
	sort.Slice(crits, func(i, j int) bool {
		return crits[i].Approx().Cmp(crits[j].Approx()) < 0
	})
	res.CriticalPoints = crits

	// Separators: 0, critical points, 1.
	seps := make([]Param, 0, len(crits)+2)
	seps = append(seps, Param{Exact: zero})
	seps = append(seps, crits...)
	seps = append(seps, Param{Exact: one})

	// Monotonicity: sign of the derivative numerator between separators.
	two := big.NewRat(2, 1)
	for k := 0; k+1 < len(seps); k++ {
		lo := seps[k].upper()
		hi := seps[k+1].lower()
		sample := new(big.Rat).Quo(new(big.Rat).Add(lo, hi), two)
		sign := d.EvalRat(sample).Sign()
		for tries := 0; sign == 0 && tries < 64; tries++ {
			// Defensive: nudge the sample toward hi if it landed
			// exactly on a root.
			sample.Quo(new(big.Rat).Add(sample, hi), two)
			sign = d.EvalRat(sample).Sign()
		}
		res.Monotonicity = append(res.Monotonicity, MonotoneInterval{
			From:       seps[k],
			To:         seps[k+1],
			Increasing: sign > 0,
		})
	}

	// Candidates with values and classification.
	for k, sep := range seps {
		t := sep.Approx()
		v := c.Eval(t)
		kind := Endpoint
		if k > 0 && k < len(seps)-1 {
			before := res.Monotonicity[k-1].Increasing
			after := res.Monotonicity[k].Increasing
			switch {
			case before && !after:
				kind = Maximum
			case !before && after:
				kind = Minimum
			default:
				kind = Stationary
			}
		}
		res.Candidates = append(res.Candidates, Extremum{T: sep, Value: v, Kind: kind})
	}

	res.Max = res.Candidates[0]
	res.Min = res.Candidates[0]
	for _, e := range res.Candidates[1:] {
		if e.Value.Cmp(res.Max.Value) > 0 {
			res.Max = e
		}
		if e.Value.Cmp(res.Min.Value) < 0 {
			res.Min = e
		}
	}
	return res
}
