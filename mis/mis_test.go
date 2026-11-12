package mis

import (
	"math"
	"math/rand"
	"testing"
)

// dir is a unit direction on the upper hemisphere, parameterised by
// cos(theta) and phi so densities are easy to evaluate analytically.
type dir struct {
	cosTheta float64
	phi      float64
}

func uniformHemisphere(r *rand.Rand) dir {
	return dir{cosTheta: r.Float64(), phi: 2 * math.Pi * r.Float64()}
}

func cosineHemisphere(r *rand.Rand) dir {
	return dir{cosTheta: math.Sqrt(r.Float64()), phi: 2 * math.Pi * r.Float64()}
}

const (
	uniformHemispherePdf = 1 / (2 * math.Pi)
)

func cosinePdf(d dir) float64 { return d.cosTheta / math.Pi }

// The squared heuristic must partition unity wherever both strategies
// have positive density: w_light + w_bsdf == 1 for every direction.
func TestPowerHeuristicPartitionUnity(t *testing.T) {
	strategies := []Strategy{{Samples: 4}, {Samples: 7}}
	r := rand.New(rand.NewSource(20261112))
	for i := 0; i < 10000; i++ {
		d := dir{cosTheta: 1e-6 + (1-1e-6)*r.Float64(), phi: 2 * math.Pi * r.Float64()}
		pdfs := []float64{uniformHemispherePdf, cosinePdf(d)}
		w0 := Weight(strategies, pdfs, 0)
		w1 := Weight(strategies, pdfs, 1)
		if math.Abs(w0+w1-1) > 1e-12 {
			t.Fatalf("weights do not sum to 1: w0=%v w1=%v", w0, w1)
		}
		if w0 <= 0 || w0 >= 1 {
			t.Fatalf("weight out of (0,1): %v", w0)
		}
	}
}

// Analytic check: integrating f(omega) = 1 over the hemisphere must
// yield 2*pi regardless of how the two strategies share the work.
// The cosine-weighted strategy alone has unbounded f/p near the
// horizon; the MIS weight must suppress that singularity.
func TestHemisphereConstantIntegral(t *testing.T) {
	const (
		nLight = 20000
		nBSDF  = 20000
		want   = 2 * math.Pi
	)
	strategies := []Strategy{{Samples: nLight}, {Samples: nBSDF}}
	r := rand.New(rand.NewSource(47))

	est := NewEstimator(strategies)
	var sumSq, sum float64
	n := 0
	record := func(c float64) {
		sum += c
		sumSq += c * c
		n++
	}
	for k := 0; k < nLight; k++ {
		d := uniformHemisphere(r)
		record(est.Add(0, 1, []float64{uniformHemispherePdf, cosinePdf(d)}))
	}
	for k := 0; k < nBSDF; k++ {
		d := cosineHemisphere(r)
		record(est.Add(1, 1, []float64{uniformHemispherePdf, cosinePdf(d)}))
	}

	got := est.Value()
	// Standard error of the per-sample contributions (both strategies
	// use equal counts here, so the pooled mean has the same scale).
	variance := sumSq/float64(n) - sq(sum/float64(n))
	stdErr := math.Sqrt(variance/float64(n)) * 2 // two strategies of n/2
	if math.Abs(got-want) > 5*stdErr {
		t.Fatalf("MIS estimate %v deviates from %v by more than 5 sigma (%v)", got, want, 5*stdErr)
	}
	// Hard deterministic bound for the fixed seed.
	if math.Abs(got-want) > 0.02 {
		t.Fatalf("MIS estimate %v outside tolerance of %v", got, want)
	}
}

// With a single strategy the MIS weight must be exactly 1 and the
// Estimator must reduce to the plain Monte Carlo mean (1/N) sum f/p.
func TestSingleStrategyDegenerate(t *testing.T) {
	const n = 5000
	strategies := []Strategy{{Samples: n}}
	r := rand.New(rand.NewSource(7))

	est := NewEstimator(strategies)
	plain := 0.0
	for k := 0; k < n; k++ {
		uniformHemisphere(r)
		pdfs := []float64{uniformHemispherePdf}
		if w := Weight(strategies, pdfs, 0); w != 1 {
			t.Fatalf("single-strategy weight = %v, want 1", w)
		}
		est.Add(0, 1, pdfs)
		plain += 1 / uniformHemispherePdf
	}
	plain /= n
	if got := est.Value(); got != plain {
		t.Fatalf("estimator %v != plain Monte Carlo %v", got, plain)
	}
	if math.Abs(plain-2*math.Pi) > 1e-12 {
		t.Fatalf("constant integrand: got %v, want 2*pi", plain)
	}
}

// A disk light of radius lr faces the shading point from distance d.
// Sampling its area uniformly and converting p_A to a solid-angle
// density with distance-squared and the light cosine must recover the
// analytic subtended solid angle Omega = 2*pi*(1 - d/sqrt(d^2+r^2))
// at every distance — verifying the conversion keeps the estimator
// unbiased as the light moves.
func TestAreaLightDistanceVariation(t *testing.T) {
	const (
		lr     = 0.5
		area   = math.Pi * lr * lr
		n      = 40000
		relTol = 0.01
	)
	r := rand.New(rand.NewSource(1234))
	for _, d := range []float64{0.6, 1, 2, 5, 20} {
		strategies := []Strategy{{Samples: n}}
		est := NewEstimator(strategies)
		for k := 0; k < n; k++ {
			// Uniform point on the disk at z = d, normal facing -z.
			rho := lr * math.Sqrt(r.Float64())
			dist2 := rho*rho + d*d
			cosLight := d / math.Sqrt(dist2)
			pdf, ok := AreaToSolidAngle(1/area, dist2, cosLight)
			if !ok {
				t.Fatalf("d=%v: valid sample rejected", d)
			}
			est.Add(0, 1, []float64{pdf})
		}
		want := 2 * math.Pi * (1 - d/math.Sqrt(d*d+lr*lr))
		if got := est.Value(); math.Abs(got-want)/want > relTol {
			t.Fatalf("d=%v: solid angle %v, want %v (rel tol %v)", d, got, want, relTol)
		}
	}
}

// Round-trip: p_A -> p_omega -> p_A must be the identity.
func TestAreaSolidAngleRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for i := 0; i < 1000; i++ {
		pA := 0.01 + 100*r.Float64()
		dist2 := 0.01 + 10*r.Float64()
		cos := 1e-3 + r.Float64()
		pW, ok := AreaToSolidAngle(pA, dist2, cos)
		if !ok {
			t.Fatal("valid input rejected")
		}
		back, ok := SolidAngleToArea(pW, dist2, cos)
		if !ok {
			t.Fatal("valid input rejected on the way back")
		}
		if math.Abs(back-pA)/pA > 1e-12 {
			t.Fatalf("round trip: %v -> %v -> %v", pA, pW, back)
		}
	}
}

// Delta events only compete with delta strategies; continuous
// strategies only with continuous ones.
func TestDiscreteIsolation(t *testing.T) {
	// A specular BSDF sample keeps weight 1 no matter how large the
	// continuous light density is at the same direction.
	strategies := []Strategy{{Samples: 1, Discrete: true}, {Samples: 1}}
	if w := Weight(strategies, []float64{1, 1e9}, 0); w != 1 {
		t.Fatalf("delta sample weight = %v, want 1", w)
	}
	// A continuous sample ignores the delta strategy's mass.
	if w := Weight(strategies, []float64{1, 0.5}, 1); w != 1 {
		t.Fatalf("continuous sample weight = %v, want 1", w)
	}
	// Two delta strategies sharing an event use the squared heuristic.
	twoDelta := []Strategy{{Samples: 1, Discrete: true}, {Samples: 1, Discrete: true}}
	w := Weight(twoDelta, []float64{0.25, 0.75}, 0)
	want := sq(0.25) / (sq(0.25) + sq(0.75))
	if math.Abs(w-want) > 1e-15 {
		t.Fatalf("delta-vs-delta weight = %v, want %v", w, want)
	}
	if got := Weight(twoDelta, []float64{0.25, 0.75}, 0) +
		Weight(twoDelta, []float64{0.25, 0.75}, 1); math.Abs(got-1) > 1e-15 {
		t.Fatalf("delta weights do not sum to 1: %v", got)
	}
}

// Zero densities, back-facing geometry, and degenerate inputs must be
// rejected explicitly rather than producing NaN or Inf contributions.
func TestRejectsInvalidContributions(t *testing.T) {
	strategies := []Strategy{{Samples: 1}, {Samples: 1}}

	if w := Weight(strategies, []float64{0, 0.5}, 0); w != 0 {
		t.Fatalf("zero generating pdf: weight = %v, want 0", w)
	}
	if w := Weight(strategies, []float64{math.NaN(), 0.5}, 0); w != 0 {
		t.Fatalf("NaN generating pdf: weight = %v, want 0", w)
	}
	if w := Weight(strategies, []float64{math.Inf(1), 0.5}, 0); w != 0 {
		t.Fatalf("Inf generating pdf: weight = %v, want 0", w)
	}
	if w := Weight(strategies, []float64{0.5, 0.5}, 5); w != 0 {
		t.Fatalf("out-of-range strategy: weight = %v, want 0", w)
	}

	est := NewEstimator(strategies)
	if c := est.Add(0, 1, []float64{0, 0.5}); c != 0 {
		t.Fatalf("zero-pdf contribution = %v, want 0", c)
	}
	if c := est.Add(0, math.NaN(), []float64{0.5, 0.5}); c != 0 {
		t.Fatalf("NaN value contribution = %v, want 0", c)
	}
	if got := est.Value(); got != 0 {
		t.Fatalf("rejected samples changed the estimate: %v", got)
	}

	cases := []struct {
		name            string
		pdf, dist2, cos float64
	}{
		{"zero pdf", 0, 1, 1},
		{"negative pdf", -1, 1, 1},
		{"NaN pdf", math.NaN(), 1, 1},
		{"zero distance", 1, 0, 1},
		{"back-facing", 1, 1, 0},
		{"grazing negative cosine", 1, 1, -0.3},
		{"NaN cosine", 1, 1, math.NaN()},
	}
	for _, c := range cases {
		if _, ok := AreaToSolidAngle(c.pdf, c.dist2, c.cos); ok {
			t.Fatalf("AreaToSolidAngle accepted %s", c.name)
		}
		if _, ok := SolidAngleToArea(c.pdf, c.dist2, c.cos); ok {
			t.Fatalf("SolidAngleToArea accepted %s", c.name)
		}
	}
}

// Sample counts scale the heuristic: a strategy with more samples is
// trusted proportionally more.
func TestSampleCountScaling(t *testing.T) {
	strategies := []Strategy{{Samples: 8}, {Samples: 1}}
	pdfs := []float64{0.5, 0.5}
	w := Weight(strategies, pdfs, 0)
	want := sq(8*0.5) / (sq(8*0.5) + sq(1*0.5))
	if math.Abs(w-want) > 1e-15 {
		t.Fatalf("count-scaled weight = %v, want %v", w, want)
	}
	if got := PowerHeuristic(8, 0.5, 1, 0.5); math.Abs(got-want) > 1e-15 {
		t.Fatalf("PowerHeuristic = %v, want %v", got, want)
	}
}
