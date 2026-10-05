package main

import (
	"math/big"
	"testing"
)

func mkCurve(vals, ws []string) Curve {
	c := Curve{}
	for _, s := range vals {
		c.Values = append(c.Values, mustRat(s))
	}
	for _, s := range ws {
		c.Weights = append(c.Weights, mustRat(s))
	}
	return c
}

func solveOK(t *testing.T, c Curve, eps string) *Solution {
	t.Helper()
	sol, err := solve(c, mustRat(eps))
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	// the certificate produced from the solution must verify
	cert := certFromSolution(sol)
	if err := verifyCert(cert); err != nil {
		t.Fatalf("verify(solve output): %v", err)
	}
	return sol
}

func containsRat(iv Interval, x string) bool {
	r := mustRat(x)
	return iv.L.Cmp(r) <= 0 && r.Cmp(iv.R) <= 0
}

func valContains(v iv, x string) bool {
	r := mustRat(x)
	return v.L.Cmp(r) <= 0 && r.Cmp(v.R) <= 0
}

// sampleAux samples the curve at many rational points (high-precision
// auxiliary evidence only — never used as a completeness proof).
func sampleAux(t *testing.T, c Curve, sol *Solution) {
	t.Helper()
	const n = 4000
	maxV := c.eval(ratI(0))
	minV := c.eval(ratI(0))
	for i := 1; i <= n; i++ {
		x := big.NewRat(int64(i), n)
		v := c.eval(x)
		if v.Cmp(maxV) > 0 {
			maxV = v
		}
		if v.Cmp(minV) < 0 {
			minV = v
		}
	}
	// true max >= sampled max and lies inside the certified enclosure
	if sol.MaxVal.R.Cmp(maxV) < 0 {
		t.Errorf("certified max upper %s below sampled max %s", sol.MaxVal.R, maxV)
	}
	if sol.MinVal.L.Cmp(minV) > 0 {
		t.Errorf("certified min lower %s above sampled min %s", sol.MinVal.L, minV)
	}
	// certified enclosure must be within eps of the sampled bound
	if new(big.Rat).Sub(sol.MaxVal.L, maxV).Cmp(sol.Eps) > 0 {
		t.Errorf("certified max lower %s too far above sampled max %s", sol.MaxVal.L, maxV)
	}
	if new(big.Rat).Sub(minV, sol.MinVal.R).Cmp(sol.Eps) > 0 {
		t.Errorf("certified min upper %s too far below sampled min %s", sol.MinVal.R, minV)
	}
}

func TestLinear(t *testing.T) {
	c := mkCurve([]string{"1", "3"}, []string{"1", "1"})
	sol := solveOK(t, c, "1/1000")
	if len(sol.Stats) != 0 {
		t.Fatalf("linear: expected no stationary points, got %d", len(sol.Stats))
	}
	if sol.Regions[0] <= 0 {
		t.Fatalf("linear: expected increasing")
	}
	if !valContains(sol.MaxVal, "3") || !valContains(sol.MinVal, "1") {
		t.Fatalf("linear: wrong extrema values")
	}
	if len(sol.MaxParams) != 1 || sol.MaxParams[0].L.Cmp(ratI(1)) != 0 {
		t.Fatalf("linear: max must be at t=1")
	}
	if len(sol.MinParams) != 1 || sol.MinParams[0].L.Sign() != 0 {
		t.Fatalf("linear: min must be at t=0")
	}
	sampleAux(t, c, sol)
}

func TestSymmetricQuadratic(t *testing.T) {
	c := mkCurve([]string{"0", "1", "0"}, []string{"1", "1", "1"})
	sol := solveOK(t, c, "1/1000")
	if len(sol.Stats) != 1 {
		t.Fatalf("expected 1 stationary point, got %d", len(sol.Stats))
	}
	st := sol.Stats[0]
	if st.Mult != 1 || st.Left != "increasing" || st.Right != "decreasing" || st.Role != "max" {
		t.Fatalf("bad stationary: %+v", st)
	}
	if !containsRat(st.Iv, "1/2") {
		t.Fatalf("stationary interval must contain 1/2")
	}
	if !valContains(sol.MaxVal, "1/2") {
		t.Fatalf("max value must enclose 1/2, got [%s,%s]", sol.MaxVal.L, sol.MaxVal.R)
	}
	// tie at both endpoints for the min
	if len(sol.MinParams) != 2 {
		t.Fatalf("min must have 2 tied params, got %d", len(sol.MinParams))
	}
	sampleAux(t, c, sol)
}

func TestWeightedQuadratic(t *testing.T) {
	// rational (non-polynomial) curve, max value 2/3 at t=1/2
	c := mkCurve([]string{"0", "1", "0"}, []string{"1", "2", "1"})
	sol := solveOK(t, c, "1/10000")
	if len(sol.Stats) != 1 || !containsRat(sol.Stats[0].Iv, "1/2") {
		t.Fatalf("expected single stationary at 1/2")
	}
	if !valContains(sol.MaxVal, "2/3") {
		t.Fatalf("max value must enclose 2/3, got [%s,%s]", sol.MaxVal.L, sol.MaxVal.R)
	}
	sampleAux(t, c, sol)
}

func TestConstant(t *testing.T) {
	c := mkCurve([]string{"2", "2", "2", "2"}, []string{"1", "2", "3", "4"})
	sol := solveOK(t, c, "1/1000")
	if !sol.Constant {
		t.Fatalf("expected constant curve")
	}
	if len(sol.MaxParams) != 1 || sol.MaxParams[0] != ivFull {
		t.Fatalf("constant: extremum must cover all of [0,1]")
	}
	if !valContains(sol.MaxVal, "2") || !valContains(sol.MinVal, "2") {
		t.Fatalf("constant: value must be 2")
	}
}

func TestRepeatedStationary(t *testing.T) {
	// B(t) = (t - 1/2)^3: derivative 3(t-1/2)^2, double root at 1/2,
	// monotone increasing; extrema only at endpoints.
	c := mkCurve([]string{"-1/8", "1/8", "-1/8", "1/8"}, []string{"1", "1", "1", "1"})
	sol := solveOK(t, c, "1/1000")
	if len(sol.Stats) != 1 {
		t.Fatalf("expected 1 stationary point, got %d", len(sol.Stats))
	}
	st := sol.Stats[0]
	if st.Mult != 2 {
		t.Fatalf("expected multiplicity 2, got %d", st.Mult)
	}
	if st.Left != "increasing" || st.Right != "increasing" || st.Role != "none" {
		t.Fatalf("double root must be a stationary inflection: %+v", st)
	}
	if !containsRat(st.Iv, "1/2") {
		t.Fatalf("stationary must be at 1/2")
	}
	if !valContains(sol.MaxVal, "1/8") || !valContains(sol.MinVal, "-1/8") {
		t.Fatalf("extrema must be at endpoints")
	}
	if len(sol.MaxParams) != 1 || sol.MaxParams[0].L.Cmp(ratI(1)) != 0 {
		t.Fatalf("max must be at t=1 only")
	}
	sampleAux(t, c, sol)
}

func TestCloseStationaryPoints(t *testing.T) {
	// B'(t) = 3(t-a)(t-b) with a=1/2, b=1/2+1/10^6 (distinct, very close).
	// Cubic Bezier control values derived by integrating in Bernstein form.
	c := mkCurve(
		[]string{"0", "500001/2000000", "1/2000000", "1/4"},
		[]string{"1", "1", "1", "1"})
	sol := solveOK(t, c, "1/10000000")
	if len(sol.Stats) != 2 {
		t.Fatalf("expected 2 stationary points, got %d", len(sol.Stats))
	}
	a, b := sol.Stats[0], sol.Stats[1]
	if !containsRat(a.Iv, "1/2") || !containsRat(b.Iv, "500001/1000000") {
		t.Fatalf("roots must be at 1/2 and 1/2+1e-6: [%s,%s] [%s,%s]",
			a.Iv.L, a.Iv.R, b.Iv.L, b.Iv.R)
	}
	// intervals must be certified disjoint
	if a.Iv.R.Cmp(b.Iv.L) >= 0 {
		t.Fatalf("isolating intervals must be disjoint")
	}
	if a.Role != "max" || b.Role != "min" {
		t.Fatalf("roles: got %s then %s", a.Role, b.Role)
	}
	if a.Left != "increasing" || a.Right != "decreasing" ||
		b.Left != "decreasing" || b.Right != "increasing" {
		t.Fatalf("monotonicity wrong")
	}
	// global max at t=1 (value 1/4), global min at t=0 (value 0)
	if !valContains(sol.MaxVal, "1/4") || !valContains(sol.MinVal, "0") {
		t.Fatalf("global extrema wrong")
	}
	sampleAux(t, c, sol)
}

func TestTiedMinima(t *testing.T) {
	// B(t) = (t-1/4)^2 (t-3/4)^2: global min 0 at t=1/4 AND t=3/4 (tie),
	// local max 1/256 at t=1/2, global max 9/256 at both endpoints (tie).
	c := mkCurve(
		[]string{"9/256", "-15/256", "59/768", "-15/256", "9/256"},
		[]string{"1", "1", "1", "1", "1"})
	sol := solveOK(t, c, "1/1000")
	if len(sol.Stats) != 3 {
		t.Fatalf("expected 3 stationary points, got %d", len(sol.Stats))
	}
	if !containsRat(sol.Stats[0].Iv, "1/4") || !containsRat(sol.Stats[1].Iv, "1/2") ||
		!containsRat(sol.Stats[2].Iv, "3/4") {
		t.Fatalf("stationary points must be 1/4, 1/2, 3/4")
	}
	// the tie must be preserved: exactly two interior minimizers
	if len(sol.MinParams) != 2 {
		t.Fatalf("min tie must keep 2 params, got %d", len(sol.MinParams))
	}
	if !containsRat(sol.MinParams[0], "1/4") || !containsRat(sol.MinParams[1], "3/4") {
		t.Fatalf("min params must contain 1/4 and 3/4")
	}
	if !valContains(sol.MinVal, "0") {
		t.Fatalf("min value must enclose 0")
	}
	// global max: tie between the two endpoints, value 9/256
	if len(sol.MaxParams) != 2 || !valContains(sol.MaxVal, "9/256") {
		t.Fatalf("max tie at endpoints with value 9/256 expected, got %d params [%s,%s]",
			len(sol.MaxParams), sol.MaxVal.L, sol.MaxVal.R)
	}
	sampleAux(t, c, sol)
}

func TestEndpointStationary(t *testing.T) {
	// B(t) = t^3: derivative 3t^2 vanishes only at the endpoint t=0.
	c := mkCurve([]string{"0", "0", "0", "1"}, []string{"1", "1", "1", "1"})
	sol := solveOK(t, c, "1/1000")
	if len(sol.Stats) != 0 {
		t.Fatalf("no interior stationary points expected")
	}
	if !valContains(sol.MaxVal, "1") || !valContains(sol.MinVal, "0") {
		t.Fatalf("extrema wrong")
	}
	sampleAux(t, c, sol)
}

func TestRationalWeightsQuintic(t *testing.T) {
	// degree 5 with nontrivial weights; cross-check with sampling only.
	c := mkCurve(
		[]string{"0", "3/2", "-1", "2", "-1/2", "1"},
		[]string{"1", "3", "2", "5", "1", "2"})
	sol := solveOK(t, c, "1/100000")
	for _, st := range sol.Stats {
		if st.Iv.width().Cmp(mustRat("1/100000")) > 0 {
			t.Fatalf("isolating interval too wide")
		}
	}
	sampleAux(t, c, sol)
}

// --- verifier must reject tampered certificates ---

func baseCert(t *testing.T) *Cert {
	c := mkCurve([]string{"0", "1", "0"}, []string{"1", "1", "1"})
	sol := solveOK(t, c, "1/1000")
	return certFromSolution(sol)
}

func TestVerifyTamperMultiplicity(t *testing.T) {
	cert := baseCert(t)
	cert.Stats[0].Multiplicity = 2
	if err := verifyCert(cert); err == nil {
		t.Fatalf("tampered multiplicity must be rejected")
	}
}

func TestVerifyTamperDropTiedParam(t *testing.T) {
	c := mkCurve(
		[]string{"9/256", "-15/256", "59/768", "-15/256", "9/256"},
		[]string{"1", "1", "1", "1", "1"})
	sol := solveOK(t, c, "1/1000")
	cert := certFromSolution(sol)
	// drop one of the two tied minimizers
	cert.Min.Params = cert.Min.Params[:1]
	if err := verifyCert(cert); err == nil {
		t.Fatalf("dropped tied parameter must be rejected")
	}
}

func TestVerifyTamperValueInterval(t *testing.T) {
	cert := baseCert(t)
	cert.Max.ValueL = "3/4" // true max is 1/2, interval no longer encloses it
	if err := verifyCert(cert); err == nil {
		t.Fatalf("wrong value interval must be rejected")
	}
}

func TestVerifyTamperMonotonicity(t *testing.T) {
	cert := baseCert(t)
	cert.Stats[0].Left = "decreasing"
	if err := verifyCert(cert); err == nil {
		t.Fatalf("flipped monotonicity must be rejected")
	}
}

func TestVerifyTamperRootCount(t *testing.T) {
	cert := baseCert(t)
	cert.Stats = cert.Stats[:0] // pretend no stationary points
	if err := verifyCert(cert); err == nil {
		t.Fatalf("wrong root count must be rejected")
	}
}
