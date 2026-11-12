package taskpkg

import (
	"math"
)

const energyEpsilon = 1e-30

// Normalized-correlation peaks at integer multiples of a period are
// numerically almost identical. Keep the shortest lag among such near-ties;
// this avoids reporting a multiple of the fundamental as a new pitch.
const periodTieEpsilon = 1e-6

// NormalizedCorrelation returns the normalized autocorrelation of x at the
// supplied positive lag. The value is in [-1, 1], or zero for an empty or
// zero-energy comparison.
func NormalizedCorrelation(x []float64, lag int) float64 {
	if lag <= 0 || lag >= len(x) {
		return 0
	}
	var dot, curEnergy, prevEnergy float64
	for i := lag; i < len(x); i++ {
		cur := x[i]
		prev := x[i-lag]
		dot += cur * prev
		curEnergy += cur * cur
		prevEnergy += prev * prev
	}
	if curEnergy <= energyEpsilon || prevEnergy <= energyEpsilon {
		return 0
	}
	denom := math.Sqrt(curEnergy * prevEnergy)
	if denom <= energyEpsilon {
		return 0
	}
	corr := dot / denom
	if corr > 1 {
		return 1
	}
	if corr < -1 {
		return -1
	}
	return corr
}

// BestPeriod searches residual streams for a common lag in [minLag, maxLag].
// Correlations are normalized per stream and then weighted by their local
// residual energy. Consequently opposite-phase left/right residuals reinforce
// rather than cancel one another. It returns lag zero when no valid lag exists.
func BestPeriod(residuals [][]float64, minLag, maxLag int) (lag int, score float64) {
	if minLag < 1 {
		minLag = 1
	}
	if maxLag < minLag {
		return 0, 0
	}

	maxUsable := 0
	for _, residual := range residuals {
		if len(residual) > maxUsable {
			maxUsable = len(residual)
		}
	}
	if maxLag >= maxUsable {
		maxLag = maxUsable - 1
	}
	if maxLag < minLag {
		return 0, 0
	}

	bestScore := math.Inf(-1)
	for candidate := minLag; candidate <= maxLag; candidate++ {
		var weightedScore, totalWeight float64
		for _, residual := range residuals {
			if candidate >= len(residual) {
				continue
			}
			corr := NormalizedCorrelation(residual, candidate)
			var weight float64
			for i := candidate; i < len(residual); i++ {
				weight += residual[i]*residual[i] + residual[i-candidate]*residual[i-candidate]
			}
			if weight <= energyEpsilon {
				continue
			}
			weightedScore += corr * weight
			totalWeight += weight
		}
		if totalWeight <= energyEpsilon {
			continue
		}
		candidateScore := weightedScore / totalWeight
		if lag == 0 || candidateScore > bestScore+periodTieEpsilon {
			bestScore = candidateScore
			lag = candidate
			score = candidateScore
		}
	}
	return lag, score
}

func rms(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var energy float64
	for _, v := range x {
		energy += v * v
	}
	if energy <= energyEpsilon || math.IsNaN(energy) || math.IsInf(energy, 0) {
		return 0
	}
	return math.Sqrt(energy / float64(len(x)))
}
