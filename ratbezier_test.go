package ratbezier

import (
	"math"
	"math/big"
	"testing"
)

func ratSlice(vals ...string) []*big.Rat {
	out := make([]*big.Rat, len(vals))
	for i, v := range vals {
		r, ok := new(big.Rat).SetString(v)
		if !ok {
			panic("bad rational literal: " + v)
		}
		out[i] = r
	}
	return out
}

func mustCurve(t *testing.T, y, w []*big.Rat) *Curve {
	t.Helper()
	c, err := NewCurve(y, w)
	if err != nil {
		t.Fatalf("NewCurve: %v", err)
	}
	return c
}

func ratEq(a *big.Rat, s string) bool {
	want, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational literal: " + s)
	}
	return a.Cmp(want) == 0
}

func floatOf(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

// The derivative numerator of y=[0,1,0], w=[1,2,1] is exactly 4 - 8t.
func TestDerivativePolyExact(t *testing.T) {
	c := mustCurve(t, ratSlice("0", "1", "0"), ratSlice("1", "2", "1"))
	p := c.DerivativePoly()
	ic := p.ToIntPoly()
	want := []int64{1, -2} // primitive integer form of 4 - 8t
	if len(ic) != len(want) {
		t.Fatalf("P(t) degree: got %v, want %v", ic, want)
	}
	for i, wv := range want {
		if ic[i].Cmp(big.NewInt(wv)) != 0 {
			t.Fatalf("P(t) = %s, want 1 - 2*t", IntPolyString(ic))
		}
	}
	if got := p.String(); got != "4 - 8*t" {
		t.Fatalf("P(t) string = %q", got)
	}
}

// IsolateRoots must recover both rational roots of 9t^2 - 9t + 2 exactly.
func TestIsolateExactRationalRoots(t *testing.T) {
	p := NewPoly(2, -9, 9) // (3t-1)(3t-2)
	roots := IsolateRoots(p)
	if len(roots) != 2 {
		t.Fatalf("got %d roots, want 2", len(roots))
	}
	if !roots[0].IsExact() || !ratEq(roots[0].Exact, "1/3") {
		t.Fatalf("root[0] = %+v, want exact 1/3", roots[0])
	}
	if !roots[1].IsExact() || !ratEq(roots[1].Exact, "2/3") {
		t.Fatalf("root[1] = %+v, want exact 2/3", roots[1])
	}
}

// The irrational root sqrt(2)/2 of 2t^2-1 must be tightly bracketed.
func TestIsolateIrrationalRoot(t *testing.T) {
	p := NewPoly(-1, 0, 2) // 2t^2 - 1
	roots := IsolateRoots(p)
	if len(roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(roots))
	}
	r := roots[0]
	if r.IsExact() {
		t.Fatalf("sqrt(2)/2 reported as exact %s", r.Exact)
	}
	want := math.Sqrt2 / 2
	if floatOf(r.Lo) >= want || floatOf(r.Hi) <= want {
		t.Fatalf("interval (%v,%v) does not contain %v", r.Lo, r.Hi, want)
	}
	width := new(big.Rat).Sub(r.Hi, r.Lo)
	limit := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Lsh(big.NewInt(1), 40))
	if width.Cmp(limit) > 0 {
		t.Fatalf("isolating interval too wide: %s", width.RatString())
	}
}

// Rational root theorem: 6t^2-5t+1 = (2t-1)(3t-1).
func TestRationalRoots(t *testing.T) {
	p := NewPoly(1, -5, 6)
	rr := RationalRoots(p)
	if len(rr) != 2 || !ratEq(rr[0], "1/3") || !ratEq(rr[1], "1/2") {
		t.Fatalf("RationalRoots = %v, want [1/3 1/2]", rr)
	}
}

// Flagship example: y=[0,1,0], w=[1,2,1] has an exact maximum 2/3 at t=1/2
// and minima 0 at both endpoints.
func TestAnalyzeExactExtremum(t *testing.T) {
	c := mustCurve(t, ratSlice("0", "1", "0"), ratSlice("1", "2", "1"))
	a := c.Analyze()

	if len(a.Critical) != 3 {
		t.Fatalf("got %d critical points, want 3", len(a.Critical))
	}
	mid := a.Critical[1]
	if !mid.IsExactT() || !ratEq(mid.T, "1/2") {
		t.Fatalf("interior critical t = %+v, want exact 1/2", mid)
	}
	if !ratEq(mid.Y, "2/3") {
		t.Fatalf("y(1/2) = %s, want 2/3", mid.Y.RatString())
	}
	if mid.Kind != "local max" || mid.Global != "global max" {
		t.Fatalf("mid classification = %q/%q", mid.Kind, mid.Global)
	}

	if len(a.Segments) != 2 ||
		a.Segments[0].Dir != "increasing" || a.Segments[1].Dir != "decreasing" {
		t.Fatalf("segments = %+v", a.Segments)
	}
	if !ratEq(a.Segments[0].From, "0") || !ratEq(a.Segments[0].To, "1/2") ||
		!ratEq(a.Segments[1].From, "1/2") || !ratEq(a.Segments[1].To, "1") {
		t.Fatalf("segment boundaries = %+v", a.Segments)
	}

	maxY, maxPts := a.Max()
	if !ratEq(maxY, "2/3") || len(maxPts) != 1 || !ratEq(maxPts[0].T, "1/2") {
		t.Fatalf("global max = %s at %+v", maxY.RatString(), maxPts)
	}
	minY, minPts := a.Min()
	if !ratEq(minY, "0") || len(minPts) != 2 {
		t.Fatalf("global min = %s at %+v, want 0 at both endpoints", minY.RatString(), minPts)
	}
}

// Monotone curve: extrema are exactly the endpoints.
func TestMonotoneCurve(t *testing.T) {
	c := mustCurve(t, ratSlice("0", "1", "3"), ratSlice("1", "1", "1"))
	a := c.Analyze()
	if len(a.Critical) != 2 {
		t.Fatalf("got %d critical points, want 2 (endpoints only)", len(a.Critical))
	}
	if len(a.Segments) != 1 || a.Segments[0].Dir != "increasing" {
		t.Fatalf("segments = %+v, want single increasing segment", a.Segments)
	}
	maxY, maxPts := a.Max()
	if !ratEq(maxY, "3") || len(maxPts) != 1 || !ratEq(maxPts[0].T, "1") {
		t.Fatalf("global max = %s at %+v, want 3 at t=1", maxY.RatString(), maxPts)
	}
	minY, minPts := a.Min()
	if !ratEq(minY, "0") || len(minPts) != 1 || !ratEq(minPts[0].T, "0") {
		t.Fatalf("global min = %s at %+v, want 0 at t=0", minY.RatString(), minPts)
	}
}

// Derivative root exactly at the endpoint t=0 must not confuse the analysis.
func TestEndpointDerivativeRoot(t *testing.T) {
	c := mustCurve(t, ratSlice("0", "0", "1"), ratSlice("1", "1", "1"))
	a := c.Analyze()
	if len(a.Critical) != 2 {
		t.Fatalf("got %d critical points, want 2", len(a.Critical))
	}
	if len(a.Segments) != 1 || a.Segments[0].Dir != "increasing" {
		t.Fatalf("segments = %+v", a.Segments)
	}
	maxY, _ := a.Max()
	minY, _ := a.Min()
	if !ratEq(maxY, "1") || !ratEq(minY, "0") {
		t.Fatalf("max=%s min=%s, want 1 and 0", maxY.RatString(), minY.RatString())
	}
}

// Irrational extremum: y=[0,2,1], w=[1,2,1] gives P(t) = 8 - 14t + 2t^2
// with the unique interior root (7 - sqrt(33))/4.
func TestIrrationalExtremum(t *testing.T) {
	c := mustCurve(t, ratSlice("0", "2", "1"), ratSlice("1", "2", "1"))
	a := c.Analyze()

	if got := IntPolyString(a.Derivative.ToIntPoly()); got != "4 - 7*t + t^2" {
		t.Fatalf("P(t) = %s, want 4 - 7*t + t^2", got)
	}
	if len(a.Critical) != 3 {
		t.Fatalf("got %d critical points, want 3", len(a.Critical))
	}
	mid := a.Critical[1]
	if mid.IsExactT() {
		t.Fatalf("expected inexact (irrational) critical point, got %s", mid.T.RatString())
	}
	wantT := (7 - math.Sqrt(33)) / 2
	if floatOf(mid.TLo) >= wantT || floatOf(mid.THi) <= wantT {
		t.Fatalf("t-interval (%s,%s) misses %v", mid.TLo.RatString(), mid.THi.RatString(), wantT)
	}
	// Reference value of y at the exact (irrational) extremum.
	n := func(t float64) float64 { return 8*t - 7*t*t }
	d := func(t float64) float64 { return 1 + 2*t - 2*t*t }
	wantY := n(wantT) / d(wantT)
	if math.Abs(floatOf(mid.Y)-wantY) > 1e-9 {
		t.Fatalf("y at extremum = %v, want %v", floatOf(mid.Y), wantY)
	}
	if mid.Kind != "local max" || mid.Global != "global max" {
		t.Fatalf("mid classification = %q/%q", mid.Kind, mid.Global)
	}
	minY, minPts := a.Min()
	if !ratEq(minY, "0") || len(minPts) != 1 || !ratEq(minPts[0].T, "0") {
		t.Fatalf("global min = %s at %+v, want 0 at t=0", minY.RatString(), minPts)
	}
}

// Cross-check the analysis against dense sampling for a non-symmetric curve.
func TestSamplingCrossCheck(t *testing.T) {
	c := mustCurve(t, ratSlice("1", "-2", "3"), ratSlice("1", "3", "2"))
	a := c.Analyze()

	const samples = 2001
	maxS, minS := math.Inf(-1), math.Inf(1)
	for i := 0; i < samples; i++ {
		tv := big.NewRat(int64(i), int64(samples-1))
		y := floatOf(c.Eval(tv))
		if y > maxS {
			maxS = y
		}
		if y < minS {
			minS = y
		}
	}
	maxY, _ := a.Max()
	minY, _ := a.Min()
	if math.Abs(floatOf(maxY)-maxS) > 1e-3 {
		t.Fatalf("analysis max %v vs sampled max %v", floatOf(maxY), maxS)
	}
	if math.Abs(floatOf(minY)-minS) > 1e-3 {
		t.Fatalf("analysis min %v vs sampled min %v", floatOf(minY), minS)
	}
	// Segments must tile [0,1] without gaps.
	if !ratEq(a.Segments[0].From, "0") || !ratEq(a.Segments[len(a.Segments)-1].To, "1") {
		t.Fatalf("segments do not cover [0,1]: %+v", a.Segments)
	}
	for i := 1; i < len(a.Segments); i++ {
		if a.Segments[i].From.Cmp(a.Segments[i-1].To) != 0 {
			t.Fatalf("gap between segments %d and %d", i-1, i)
		}
	}
}

// A constant curve has a zero derivative polynomial and max == min.
func TestConstantCurve(t *testing.T) {
	c := mustCurve(t, ratSlice("2", "2", "2"), ratSlice("1", "2", "1"))
	a := c.Analyze()
	if !a.Derivative.IsZero() {
		t.Fatalf("derivative = %s, want zero polynomial", a.Derivative)
	}
	if len(a.Segments) != 1 || a.Segments[0].Dir != "constant" {
		t.Fatalf("segments = %+v", a.Segments)
	}
	maxY, _ := a.Max()
	minY, _ := a.Min()
	if !ratEq(maxY, "2") || !ratEq(minY, "2") {
		t.Fatalf("max=%s min=%s, want 2 and 2", maxY.RatString(), minY.RatString())
	}
}

// NewCurve must reject mismatched or degenerate input.
func TestNewCurveValidation(t *testing.T) {
	if _, err := NewCurve(ratSlice("1", "2"), ratSlice("1")); err == nil {
		t.Fatal("expected error for length mismatch")
	}
	if _, err := NewCurve(ratSlice("1"), ratSlice("1")); err == nil {
		t.Fatal("expected error for single control point")
	}
	if _, err := NewCurve(ratSlice("1", "2"), ratSlice("1", "0")); err == nil {
		t.Fatal("expected error for zero weight")
	}
}
