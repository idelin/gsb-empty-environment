// Package mis implements multiple importance sampling (MIS) helpers for
// a Monte Carlo path integrator. It combines samples drawn from several
// strategies — typically light-source sampling and BSDF (material)
// sampling — with the power heuristic (beta = 2), so that radiance
// arriving along a path reachable by both strategies is weighted rather
// than double counted.
//
// All densities handed to this package must be expressed against the
// same measure. For direct lighting that measure is solid angle at the
// shading point; densities authored per unit area on a light source
// must be converted with AreaToSolidAngle before they are combined.
package mis

import "math"

// Strategy describes one sampling technique taking part in MIS.
type Strategy struct {
	// Samples is the number of samples drawn from this strategy in
	// each estimation (n_i in Veach's notation).
	Samples int
	// Discrete reports whether the strategy samples a delta
	// distribution (point/directional lights, perfectly specular
	// BSDF lobes). A delta event is only ever compared against other
	// delta strategies: a continuous strategy has zero probability of
	// generating the exact same event, and a delta strategy has zero
	// density at a continuous sample point.
	Discrete bool
}

// Weight returns the power-heuristic (squared) weight for a sample
// drawn from strategy i.
//
// pdfs[j] must hold strategy j's density evaluated at the sample point,
// every entry measured against the same (solid-angle) measure; for a
// discrete strategy it holds the discrete probability mass. Strategies
// of the other kind (discrete vs. continuous) are excluded from the
// denominator, as are strategies with zero samples or non-positive
// density at the point.
//
// A non-positive, NaN, or infinite generating density — or an
// out-of-range index — yields 0, so invalid contributions are dropped
// instead of amplified.
func Weight(strategies []Strategy, pdfs []float64, i int) float64 {
	if i < 0 || i >= len(strategies) || i >= len(pdfs) {
		return 0
	}
	gen := strategies[i]
	if gen.Samples <= 0 || !positiveFinite(pdfs[i]) {
		return 0
	}
	num := sq(float64(gen.Samples) * pdfs[i])
	den := 0.0
	for j, st := range strategies {
		if st.Discrete != gen.Discrete || st.Samples <= 0 {
			continue
		}
		if !positiveFinite(pdfs[j]) {
			continue
		}
		den += sq(float64(st.Samples) * pdfs[j])
	}
	if den <= 0 {
		return 0
	}
	return num / den
}

// PowerHeuristic is the two-strategy specialisation of the squared
// heuristic: nf samples drawn at density fPdf compete against ng
// samples at density gPdf. It equals Weight for two continuous
// strategies and is provided for the common light-vs-BSDF call site.
func PowerHeuristic(nf int, fPdf float64, ng int, gPdf float64) float64 {
	if nf <= 0 || !positiveFinite(fPdf) {
		return 0
	}
	f := sq(float64(nf) * fPdf)
	g := 0.0
	if ng > 0 && positiveFinite(gPdf) {
		g = sq(float64(ng) * gPdf)
	}
	if f+g <= 0 {
		return 0
	}
	return f / (f + g)
}

// Estimator accumulates the MIS combination
//
//	F = sum_i (1/n_i) sum_k w_i(x_ik) f(x_ik) / p_i(x_ik)
//
// over the samples of every strategy. With a single strategy it
// degenerates to the plain Monte Carlo estimator.
type Estimator struct {
	strategies []Strategy
	sums       []float64
}

// NewEstimator returns an Estimator for the given strategies. The slice
// is copied and must not be mutated by the caller afterwards.
func NewEstimator(strategies []Strategy) *Estimator {
	cp := make([]Strategy, len(strategies))
	copy(cp, strategies)
	return &Estimator{strategies: cp, sums: make([]float64, len(cp))}
}

// Add folds one sample into the estimate. strategy identifies the
// strategy that generated the sample, value is the integrand f(x)
// (e.g. Li * BSDF * cosTheta for direct lighting), and pdfs holds every
// strategy's density at x — pdfs[strategy] is the generating density.
//
// The weighted contribution w*f/p is returned; it is exactly 0 for
// invalid samples (non-positive or non-finite generating density,
// non-finite value, unknown strategy), which are rejected instead of
// contaminating the estimate.
func (e *Estimator) Add(strategy int, value float64, pdfs []float64) float64 {
	w := Weight(e.strategies, pdfs, strategy)
	if w == 0 || !finite(value) {
		return 0
	}
	pdf := pdfs[strategy]
	c := w * value / pdf
	if !finite(c) {
		return 0
	}
	e.sums[strategy] += c
	return c
}

// Value returns the combined estimate: each strategy's accumulated
// weighted contributions normalised by its declared sample count.
func (e *Estimator) Value() float64 {
	total := 0.0
	for i, st := range e.strategies {
		if st.Samples <= 0 {
			continue
		}
		total += e.sums[i] / float64(st.Samples)
	}
	return total
}

// AreaToSolidAngle converts a density p_A measured per unit area on a
// light source into the equivalent solid-angle density at the shading
// point:
//
//	p_omega = p_A * d^2 / |cos theta_l|
//
// where d^2 = dist2 is the squared distance from the shading point to
// the sampled point on the light and theta_l is the angle between the
// light's surface normal and the direction back toward the shading
// point.
//
// ok is false — and the caller must discard the contribution — when the
// sample is back-facing (cosLight <= 0), degenerate (dist2 <= 0), or
// the area density is not positive and finite.
func AreaToSolidAngle(areaPdf, dist2, cosLight float64) (pdf float64, ok bool) {
	if !positiveFinite(areaPdf) || !positiveFinite(dist2) || !positiveFinite(cosLight) {
		return 0, false
	}
	pdf = areaPdf * dist2 / cosLight
	if !positiveFinite(pdf) {
		return 0, false
	}
	return pdf, true
}

// SolidAngleToArea is the inverse of AreaToSolidAngle, converting a
// solid-angle density back to a per-unit-area density:
//
//	p_A = p_omega * |cos theta_l| / d^2
//
// The same rejection rules apply.
func SolidAngleToArea(solidPdf, dist2, cosLight float64) (pdf float64, ok bool) {
	if !positiveFinite(solidPdf) || !positiveFinite(dist2) || !positiveFinite(cosLight) {
		return 0, false
	}
	pdf = solidPdf * cosLight / dist2
	if !positiveFinite(pdf) {
		return 0, false
	}
	return pdf, true
}

func sq(x float64) float64 { return x * x }

func positiveFinite(x float64) bool {
	return x > 0 && !math.IsNaN(x) && !math.IsInf(x, 0)
}

func finite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}
