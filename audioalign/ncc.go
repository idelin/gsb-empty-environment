package audioalign

import "math"

// windowCorrelations evaluates every feasible integer lag for the window
// starting at start in ref. It returns false when the window is silent in
// either track or has no overlapping samples at any configured lag.
func windowCorrelations(ref, delayed []float64, start int, cfg Config) (map[int]float64, bool) {
	end := start + cfg.WindowSize
	if end > len(ref) {
		return nil, false
	}
	winRef := ref[start:end]
	if rms(winRef) < cfg.SilenceRMS {
		return nil, false
	}

	corr := make(map[int]float64)
	for lag := cfg.MinLag; lag <= cfg.MaxLag; lag++ {
		dStart := start + lag
		dEnd := end + lag
		if dStart < 0 || dEnd > len(delayed) {
			continue
		}
		winDel := delayed[dStart:dEnd]
		if rms(winDel) < cfg.SilenceRMS {
			continue
		}
		c, ok := normalizedCorrelation(winRef, winDel)
		if !ok {
			continue
		}
		corr[lag] = c
	}
	if len(corr) == 0 {
		return nil, false
	}
	return corr, true
}

// rms is the mean-removed root mean square of a window.
func rms(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += v
	}
	mean := sum / float64(len(x))

	var acc float64
	for _, v := range x {
		d := v - mean
		acc += d * d
	}
	return math.Sqrt(acc / float64(len(x)))
}

// normalizedCorrelation is the Pearson correlation of two equal-length
// windows. ok is false when either window has zero variance.
func normalizedCorrelation(a, b []float64) (c float64, ok bool) {
	n := len(a)
	if n != len(b) || n == 0 {
		return 0, false
	}
	var sa, sb float64
	for i := 0; i < n; i++ {
		sa += a[i]
		sb += b[i]
	}
	ma := sa / float64(n)
	mb := sb / float64(n)

	var dot, va, vb float64
	for i := 0; i < n; i++ {
		x := a[i] - ma
		y := b[i] - mb
		dot += x * y
		va += x * x
		vb += y * y
	}
	if va == 0 || vb == 0 {
		return 0, false
	}
	c = dot / math.Sqrt(va*vb)
	// Clamp tiny floating-point overshoots into the valid correlation range.
	if c > 1 {
		c = 1
	} else if c < -1 {
		c = -1
	}
	return c, true
}

// parabolicDelta estimates the fractional offset of an NCC peak at lag by
// fitting a parabola through the correlations at lag-1, lag and lag+1.
// The result lies in [-0.5, 0.5]; it is zero when the neighbors are absent
// or the quadratic is not concave.
func parabolicDelta(lag int, corr map[int]float64) float64 {
	lo, hasLo := corr[lag-1]
	mid, hasMid := corr[lag]
	hi, hasHi := corr[lag+1]
	if !hasLo || !hasMid || !hasHi {
		return 0
	}
	denom := lo - 2*mid + hi
	if denom >= 0 {
		return 0
	}
	delta := 0.5 * (lo - hi) / denom
	if delta > 0.5 {
		delta = 0.5
	} else if delta < -0.5 {
		delta = -0.5
	}
	return delta
}
