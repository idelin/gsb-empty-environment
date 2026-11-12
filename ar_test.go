package taskpkg

import (
	"math"
	"math/rand"
	"testing"
)

func TestAutocorrelationUsesFixedWindowDivisor(t *testing.T) {
	x := []float64{1, 2, 3, 4}
	r := Autocorrelation(x, 3)
	want := []float64{7.5, 5, 2.75, 1}
	for i := range want {
		if math.Abs(r[i]-want[i]) > 1e-12 {
			t.Fatalf("r[%d] = %g, want %g", i, r[i], want[i])
		}
	}
}

func TestLevinsonDurbinStopsBeforeUnitReflection(t *testing.T) {
	model := LevinsonDurbin([]float64{1, 0.5, 1.0}, 2)
	if model.Order != 1 || len(model.Coefficients) != 1 || len(model.Reflection) != 1 {
		t.Fatalf("expected stable order 1, got %+v", model)
	}
	if math.Abs(model.Coefficients[0]-0.5) > 1e-12 {
		t.Fatalf("coefficient = %g, want 0.5", model.Coefficients[0])
	}

	if got := LevinsonDurbin([]float64{1, 2}, 1); got.Order != 0 || len(got.Coefficients) != 0 || got.Error != 1 {
		t.Fatalf("expected order zero for reflection above one, got %+v", got)
	}
}

func TestFitARRecoversStableSignalAndResidual(t *testing.T) {
	const (
		a1    = 0.90
		a2    = -0.20
		sigma = 0.05
	)
	rng := rand.New(rand.NewSource(9))
	x := make([]float64, 4096)
	prev2, prev1 := 0.0, 0.0
	for i := range x {
		e := rng.NormFloat64() * sigma
		x[i] = a1*prev1 + a2*prev2 + e
		prev2, prev1 = prev1, x[i]
	}

	model := FitAR(x, 2048, 2)
	if model.Order != 2 {
		t.Fatalf("order = %d, want 2 (%+v)", model.Order, model)
	}
	if math.Abs(model.Coefficients[0]-a1) > 0.04 || math.Abs(model.Coefficients[1]-a2) > 0.04 {
		t.Fatalf("coefficients = %v, want approximately [%g %g]", model.Coefficients, a1, a2)
	}

	residual := Residual(x[2000:], model.Coefficients)
	if got := rms(residual); math.Abs(got-sigma) > 0.01 {
		t.Fatalf("residual RMS = %g, want about %g", got, sigma)
	}
	for _, k := range model.Reflection {
		if math.Abs(k) >= 1 {
			t.Fatalf("unstable reflection coefficient %g", k)
		}
	}
}

func TestResidualWithKnownCoefficient(t *testing.T) {
	x := []float64{1, 2, 4, 7}
	got := Residual(x, []float64{1})
	want := []float64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("residual length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("residual[%d] = %g, want %g", i, got[i], want[i])
		}
	}
}

func TestNormalizedCorrelationAndBestPeriod(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	base := make([]float64, 500)
	for i := range base {
		base[i] = rng.NormFloat64()
	}
	const period = 17
	x := make([]float64, 120)
	for i := range x {
		x[i] = base[i%period]
	}

	if got := NormalizedCorrelation(x, period); got < 0.999 {
		t.Fatalf("correlation at true period = %g, want near 1", got)
	}
	lag, score := BestPeriod([][]float64{x, negate(x)}, 8, 24)
	if lag != period || score < 0.99 {
		t.Fatalf("BestPeriod = (%d, %g), want (%d, near 1)", lag, score, period)
	}
}

func negate(x []float64) []float64 {
	out := make([]float64, len(x))
	for i := range x {
		out[i] = -x[i]
	}
	return out
}
