package analyze

import "math"

// ChiSquarePValue returns the survival function (upper-tail p-value) of
// the chi-square distribution with the given degrees of freedom:
// p = Q(df/2, x/2). A small p-value means the observed counts are
// inconsistent with the expected distribution.
func ChiSquarePValue(df int, x float64) float64 {
	if df <= 0 || x < 0 {
		return math.NaN()
	}
	return 1 - gammaP(0.5*float64(df), 0.5*x)
}

// ChiSquareCritical returns the critical value of the chi-square
// distribution: the smallest x with ChiSquarePValue(df, x) <= alpha.
func ChiSquareCritical(df int, alpha float64) float64 {
	lo, hi := 0.0, float64(df)
	for ChiSquarePValue(df, hi) > alpha {
		hi *= 2
	}
	for i := 0; i < 200; i++ {
		mid := 0.5 * (lo + hi)
		if ChiSquarePValue(df, mid) > alpha {
			lo = mid
		} else {
			hi = mid
		}
	}
	return 0.5 * (lo + hi)
}

// gammaP is the regularized lower incomplete gamma function P(a, x),
// computed with the series expansion for x < a+1 and with the continued
// fraction for Q(a, x) = 1 - P(a, x) otherwise (Numerical Recipes).
func gammaP(a, x float64) float64 {
	if a <= 0 || x < 0 {
		return math.NaN()
	}
	if x == 0 {
		return 0
	}
	lg, _ := math.Lgamma(a)
	if x < a+1 {
		sum := 1.0 / a
		term := sum
		for n := 1; n < 10000; n++ {
			term *= x / (a + float64(n))
			sum += term
			if math.Abs(term) < math.Abs(sum)*1e-15 {
				break
			}
		}
		return sum * math.Exp(-x+a*math.Log(x)-lg)
	}
	// Continued fraction for Q(a, x).
	const tiny = 1e-290
	b := x + 1 - a
	c := 1 / tiny
	d := 1 / b
	h := d
	for i := 1; i < 10000; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < 1e-15 {
			break
		}
	}
	q := math.Exp(-x+a*math.Log(x)-lg) * h
	return 1 - q
}
