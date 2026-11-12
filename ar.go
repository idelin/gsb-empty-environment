package taskpkg

import (
	"math"
)

// ARModel is a stable forward linear predictor:
//
//	x[n] ~= sum(Coefficients[j-1] * x[n-j], j=1..Order)
//
// Reflection holds only coefficients accepted by Levinson-Durbin. If an
// intermediate reflection coefficient has magnitude greater than or equal to
// one, the recursion stops at the preceding stable order.
type ARModel struct {
	Order        int
	Coefficients []float64
	Reflection   []float64
	Error        float64
}

// Autocorrelation computes
//
//	r[k] = sum(x[i]*x[i-k], i=k..len(x)-1) / len(x)
//
// for k in [0, maxLag]. It deliberately divides every lag by the full window
// length rather than by the number of overlapping products. Lags at or beyond
// len(x) are zero.
func Autocorrelation(x []float64, maxLag int) []float64 {
	if maxLag < 0 {
		maxLag = 0
	}
	r := make([]float64, maxLag+1)
	n := len(x)
	if n == 0 {
		return r
	}
	denom := float64(n)
	for k := 0; k <= maxLag && k < n; k++ {
		var sum float64
		for i := k; i < n; i++ {
			sum += x[i] * x[i-k]
		}
		r[k] = sum / denom
	}
	return r
}

// LevinsonDurbin solves the normal equations for a forward predictor from
// autocorrelation r. The recursion stops before accepting a reflection
// coefficient whose absolute value is one or larger. r[0] must be non-zero
// for an order greater than zero.
func LevinsonDurbin(r []float64, order int) ARModel {
	if order < 0 {
		order = 0
	}
	if order > len(r)-1 {
		order = len(r) - 1
	}
	if len(r) == 0 || r[0] <= 0 || math.IsNaN(r[0]) || math.IsInf(r[0], 0) {
		return ARModel{}
	}

	a := make([]float64, order) // a[j-1] multiplies x[n-j]
	prev := make([]float64, order)
	refl := make([]float64, 0, order)
	errEnergy := r[0]
	stableOrder := 0
	if order == 0 {
		return ARModel{Error: errEnergy}
	}

	for m := 1; m <= order; m++ {
		if errEnergy <= 0 || math.IsNaN(errEnergy) || math.IsInf(errEnergy, 0) {
			break
		}

		num := r[m]
		for j := 1; j < m; j++ {
			num -= a[j-1] * r[m-j]
		}
		k := num / errEnergy
		if math.IsNaN(k) || math.IsInf(k, 0) || math.Abs(k) >= 1 {
			break
		}

		copy(prev[:m], a[:m])
		for j := 1; j < m; j++ {
			a[j-1] = prev[j-1] - k*prev[m-j-1]
		}
		a[m-1] = k
		errEnergy *= 1 - k*k
		if errEnergy < 0 && errEnergy > -1e-18 {
			errEnergy = 0
		}
		refl = append(refl, k)
		stableOrder = m
	}

	return ARModel{
		Order:        stableOrder,
		Coefficients: append([]float64(nil), a[:stableOrder]...),
		Reflection:   refl,
		Error:        errEnergy,
	}
}

// Residual returns the prediction residual for the portion of x covered by
// len(coeff) samples of history. The result has len(x)-len(coeff) samples.
func Residual(x []float64, coeff []float64) []float64 {
	p := len(coeff)
	if p == 0 {
		return append([]float64(nil), x...)
	}
	if len(x) <= p {
		return nil
	}
	out := make([]float64, len(x)-p)
	for i := p; i < len(x); i++ {
		pred := 0.0
		for j := 1; j <= p; j++ {
			pred += coeff[j-1] * x[i-j]
		}
		out[i-p] = x[i] - pred
	}
	return out
}

// FitAR estimates the predictor for the most recent window samples in x.
func FitAR(x []float64, window, order int) ARModel {
	if window <= 0 || len(x) == 0 {
		return ARModel{}
	}
	if len(x) > window {
		x = x[len(x)-window:]
	}
	if order >= len(x) {
		order = len(x) - 1
	}
	if order < 0 {
		order = 0
	}
	r := Autocorrelation(x, order)
	return LevinsonDurbin(r, order)
}

func predict(coeff []float64, history *ringBuffer) float64 {
	if len(coeff) == 0 || history.count < len(coeff) {
		return 0
	}
	var pred float64
	for j := 1; j <= len(coeff); j++ {
		pred += coeff[j-1] * history.fromEnd(j-1)
	}
	return pred
}
