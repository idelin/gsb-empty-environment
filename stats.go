package hashring

import "math"

// ChiSquarePValue returns the survival function P(X >= chi2) of the
// chi-square distribution with df degrees of freedom.
func ChiSquarePValue(chi2 float64, df int) float64 {
	return gammaQ(float64(df)/2, chi2/2)
}

// gammaQ is the regularized upper incomplete gamma function Q(a, x),
// computed via series expansion / continued fraction (Numerical Recipes).
func gammaQ(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	if x == 0 {
		return 1
	}
	lg, _ := math.Lgamma(a)
	if x < a+1 {
		// Series for P(a,x), then Q = 1 - P.
		ap := a
		sum := 1.0 / a
		del := sum
		for i := 0; i < 10000; i++ {
			ap++
			del *= x / ap
			sum += del
			if math.Abs(del) < math.Abs(sum)*1e-15 {
				break
			}
		}
		p := sum * math.Exp(-x+a*math.Log(x)-lg)
		return 1 - p
	}
	// Continued fraction for Q(a,x).
	b := x + 1 - a
	c := 1e300
	d := 1 / b
	h := d
	for i := 1; i < 10000; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < 1e-300 {
			d = 1e-300
		}
		c = b + an/c
		if math.Abs(c) < 1e-300 {
			c = 1e-300
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-15 {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-lg) * h
}

// ChiSquare computes the Pearson chi-square statistic of observed counts
// against the uniform expectation.
func ChiSquare(counts []int) (chi2 float64, df int) {
	var total int64
	for _, c := range counts {
		total += int64(c)
	}
	expected := make([]float64, len(counts))
	for i := range expected {
		expected[i] = float64(total) / float64(len(counts))
	}
	return ChiSquareExpected(counts, expected)
}

// ChiSquareExpected computes the Pearson chi-square statistic of observed
// counts against explicit per-bucket expectations.
func ChiSquareExpected(counts []int, expected []float64) (chi2 float64, df int) {
	for i, c := range counts {
		d := float64(c) - expected[i]
		chi2 += d * d / expected[i]
	}
	return chi2, len(counts) - 1
}
