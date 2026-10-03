package ratbezier

import (
	"math"
	"math/big"
	"testing"
)

func mustCurve(t *testing.T, points, weights []float64) *Curve {
	t.Helper()
	c, err := NewCurve(points, weights)
	if err != nil {
		t.Fatalf("NewCurve(%v, %v): %v", points, weights, err)
	}
	return c
}

func ratFloat(r *big.Rat) float64 {
	f, _ := new(big.Float).SetPrec(256).SetRat(r).Float64()
	return f
}

func TestDerivativePolynomial(t *testing.T) {
	// points (0,2,1), weights (1,2,1):
	//   P(t) = 8t - 7t^2, W(t) = 1 + 2t - 2t^2
	//   N(t) = P'W - PW' = 8 - 14t + 2t^2, primitive: 4 - 7t + t^2
	c := mustCurve(t, []float64{0, 2, 1}, []float64{1, 2, 1})
	if got := c.DerivativePolynomial(); !polyEq(got, 4, -7, 1) {
		t.Errorf("DerivativePolynomial = %s, want t^2 - 7t + 4", got)
	}
}

func TestEvalExact(t *testing.T) {
	c := mustCurve(t, []float64{0, 1, 0}, []float64{1, 2, 1})
	cases := []struct {
		t    *big.Rat
		want *big.Rat
	}{
		{big.NewRat(0, 1), big.NewRat(0, 1)},
		{big.NewRat(1, 2), big.NewRat(2, 3)},
		{big.NewRat(1, 1), big.NewRat(0, 1)},
	}
	for _, tc := range cases {
		if got := c.Eval(tc.t); got.Cmp(tc.want) != 0 {
			t.Errorf("Eval(%s) = %s, want %s", tc.t, got.RatString(), tc.want.RatString())
		}
	}
}

func TestAnalyzeExactRationalCriticalPoint(t *testing.T) {
	c := mustCurve(t, []float64{0, 1, 0}, []float64{1, 2, 1})
	res := c.Analyze(nil)

	if res.Constant {
		t.Fatal("curve should not be constant")
	}
	if !polyEq(res.Derivative, 1, -2) {
		t.Errorf("derivative = %s, want 1 - 2t", res.Derivative)
	}
	if len(res.CriticalPoints) != 1 {
		t.Fatalf("got %d critical points, want 1", len(res.CriticalPoints))
	}
	cp := res.CriticalPoints[0]
	if !cp.IsExact() || cp.Exact.Cmp(big.NewRat(1, 2)) != 0 {
		t.Errorf("critical point = %s, want exact 1/2", cp)
	}

	// Global maximum 2/3 at t = 1/2, global minimum 0 at both endpoints.
	if res.Max.Value.Cmp(big.NewRat(2, 3)) != 0 {
		t.Errorf("max value = %s, want 2/3", res.Max.Value.RatString())
	}
	if res.Max.T.Approx().Cmp(big.NewRat(1, 2)) != 0 {
		t.Errorf("max at t = %s, want 1/2", res.Max.T)
	}
	if res.Min.Value.Sign() != 0 {
		t.Errorf("min value = %s, want 0", res.Min.Value.RatString())
	}

	// Monotonicity: increasing on [0, 1/2], decreasing on [1/2, 1].
	if len(res.Monotonicity) != 2 {
		t.Fatalf("got %d monotone intervals, want 2", len(res.Monotonicity))
	}
	if !res.Monotonicity[0].Increasing || res.Monotonicity[1].Increasing {
		t.Errorf("monotonicity = %+v, want increasing then decreasing", res.Monotonicity)
	}

	// Candidate classification: max in the middle, endpoints on both sides.
	if len(res.Candidates) != 3 {
		t.Fatalf("got %d candidates, want 3", len(res.Candidates))
	}
	if res.Candidates[0].Kind != Endpoint || res.Candidates[1].Kind != Maximum || res.Candidates[2].Kind != Endpoint {
		t.Errorf("candidate kinds = %v, %v, %v", res.Candidates[0].Kind, res.Candidates[1].Kind, res.Candidates[2].Kind)
	}
}

func TestAnalyzeIrrationalCriticalPoint(t *testing.T) {
	c := mustCurve(t, []float64{0, 2, 1}, []float64{1, 2, 1})
	res := c.Analyze(nil)

	if len(res.CriticalPoints) != 1 {
		t.Fatalf("got %d critical points, want 1", len(res.CriticalPoints))
	}
	cp := res.CriticalPoints[0]
	if cp.IsExact() {
		t.Fatalf("critical point should be irrational, got exact %s", cp.Exact)
	}

	// True root: (7 - sqrt(33)) / 2.
	want := (7 - math.Sqrt(33)) / 2
	got := ratFloat(cp.Approx())
	if math.Abs(got-want) > 1e-15 {
		t.Errorf("critical point approx %.18g, want %.18g", got, want)
	}
	// Isolating interval must respect the default precision 1e-30.
	width := new(big.Rat).Sub(cp.Hi, cp.Lo)
	if width.Cmp(DefaultPrecision()) > 0 {
		t.Errorf("isolating interval width %s exceeds 1e-30", width.RatString())
	}
	// The isolating interval must bracket a sign change of N (exact check).
	if res.Derivative.EvalRat(cp.Lo).Sign() == res.Derivative.EvalRat(cp.Hi).Sign() {
		t.Errorf("no sign change of N across isolating interval %s", cp)
	}

	// Global maximum at the critical point; value matches direct evaluation.
	if res.Max.T.Approx().Cmp(cp.Approx()) != 0 {
		t.Errorf("global max not at critical point")
	}
	wantVal := (8*want - 7*want*want) / (1 + 2*want - 2*want*want)
	if gotVal := ratFloat(res.Max.Value); math.Abs(gotVal-wantVal) > 1e-12 {
		t.Errorf("max value %.15g, want %.15g", gotVal, wantVal)
	}
	if res.Max.Value.Cmp(big.NewRat(1, 1)) <= 0 {
		t.Errorf("max value %s should exceed endpoint value 1", res.Max.Value.RatString())
	}

	// Global minimum 0 at t = 0.
	if res.Min.Value.Sign() != 0 || res.Min.T.Approx().Sign() != 0 {
		t.Errorf("min = %s at t = %s, want 0 at t = 0", res.Min.Value.RatString(), res.Min.T)
	}

	// Monotonicity: increasing then decreasing, covering [0, 1].
	if len(res.Monotonicity) != 2 ||
		!res.Monotonicity[0].Increasing || res.Monotonicity[1].Increasing {
		t.Fatalf("monotonicity = %+v", res.Monotonicity)
	}
	if res.Monotonicity[0].From.Approx().Sign() != 0 {
		t.Errorf("first interval does not start at 0")
	}
	if res.Monotonicity[1].To.Approx().Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("last interval does not end at 1")
	}
}

func TestAnalyzeMonotone(t *testing.T) {
	c := mustCurve(t, []float64{0, 1, 2}, []float64{1, 1, 1})
	res := c.Analyze(nil)

	if len(res.CriticalPoints) != 0 {
		t.Fatalf("got %d critical points, want 0", len(res.CriticalPoints))
	}
	if len(res.Monotonicity) != 1 || !res.Monotonicity[0].Increasing {
		t.Fatalf("monotonicity = %+v, want single increasing interval", res.Monotonicity)
	}
	if res.Max.Value.Cmp(big.NewRat(2, 1)) != 0 || res.Max.T.Approx().Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("max = %s at t = %s, want 2 at t = 1", res.Max.Value.RatString(), res.Max.T)
	}
	if res.Min.Value.Sign() != 0 || res.Min.T.Approx().Sign() != 0 {
		t.Errorf("min = %s at t = %s, want 0 at t = 0", res.Min.Value.RatString(), res.Min.T)
	}
	// Both endpoints are always present as candidates.
	if len(res.Candidates) != 2 || res.Candidates[0].Kind != Endpoint || res.Candidates[1].Kind != Endpoint {
		t.Errorf("candidates = %+v, want the two endpoints", res.Candidates)
	}
}

func TestAnalyzeDecreasing(t *testing.T) {
	c := mustCurve(t, []float64{2, 1, 0}, []float64{1, 1, 1})
	res := c.Analyze(nil)
	if len(res.Monotonicity) != 1 || res.Monotonicity[0].Increasing {
		t.Fatalf("monotonicity = %+v, want single decreasing interval", res.Monotonicity)
	}
	if res.Max.T.Approx().Sign() != 0 || res.Max.Value.Cmp(big.NewRat(2, 1)) != 0 {
		t.Errorf("max = %s at t = %s, want 2 at t = 0", res.Max.Value.RatString(), res.Max.T)
	}
	if res.Min.T.Approx().Cmp(big.NewRat(1, 1)) != 0 || res.Min.Value.Sign() != 0 {
		t.Errorf("min = %s at t = %s, want 0 at t = 1", res.Min.Value.RatString(), res.Min.T)
	}
}

func TestAnalyzeConstant(t *testing.T) {
	c := mustCurve(t, []float64{3, 3, 3}, []float64{1, 2, 1})
	res := c.Analyze(nil)
	if !res.Constant {
		t.Fatal("curve should be detected as constant")
	}
	if res.Max.Value.Cmp(big.NewRat(3, 1)) != 0 || res.Min.Value.Cmp(big.NewRat(3, 1)) != 0 {
		t.Errorf("max = %s, min = %s, want both 3", res.Max.Value.RatString(), res.Min.Value.RatString())
	}
}

func TestAnalyzeHigherDegree(t *testing.T) {
	// Cubic rational Bezier with two interior extrema:
	// derivative numerator has degree 4.
	c := mustCurve(t, []float64{0, 2, -1, 1}, []float64{1, 2, 3, 1})
	res := c.Analyze(nil)
	if res.Derivative.Degree() > 4 {
		t.Errorf("derivative degree = %d, want <= 4", res.Derivative.Degree())
	}
	// Every critical point must make the derivative numerator vanish
	// (approximately, within the isolating interval).
	for _, cp := range res.CriticalPoints {
		if cp.IsExact() {
			if res.Derivative.EvalRat(cp.Exact).Sign() != 0 {
				t.Errorf("exact critical point %s is not a root of N", cp.Exact)
			}
			continue
		}
		if res.Derivative.EvalRat(cp.Lo).Sign() == res.Derivative.EvalRat(cp.Hi).Sign() {
			t.Errorf("no sign change across isolating interval %s", cp)
		}
	}
	// Global max/min values must bracket all candidate values.
	for _, e := range res.Candidates {
		if e.Value.Cmp(res.Max.Value) > 0 || e.Value.Cmp(res.Min.Value) < 0 {
			t.Errorf("candidate value %s outside [min, max]", e.Value.RatString())
		}
	}
}

func TestNewCurveValidation(t *testing.T) {
	if _, err := NewCurve([]float64{0, 1}, []float64{1, 1, 1}); err == nil {
		t.Error("expected error for mismatched lengths")
	}
	if _, err := NewCurve([]float64{0}, []float64{1}); err == nil {
		t.Error("expected error for a single control point")
	}
	if _, err := NewCurve([]float64{0, 1, 2}, []float64{1, 0, 1}); err == nil {
		t.Error("expected error for zero weight")
	}
	if _, err := NewCurve([]float64{0, 1, 2}, []float64{1, -1, 1}); err == nil {
		t.Error("expected error for negative weight")
	}
	if _, err := NewCurve([]float64{0, math.NaN(), 2}, []float64{1, 1, 1}); err == nil {
		t.Error("expected error for NaN control point")
	}
}
