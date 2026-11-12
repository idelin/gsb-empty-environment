package taskpkg

import (
	"errors"
	"fmt"
	"math"
)

// PairState describes why a channel pair did or did not become graph evidence.
type PairState string

const (
	PairEdge             PairState = "edge"
	PairSilent           PairState = "silent"
	PairWeakCorrelation  PairState = "weak_correlation"
	PairEnergyOutOfRange PairState = "energy_ratio_out_of_range"
)

// Unknown reports whether the pair cannot be used as polarity evidence.
func (s PairState) Unknown() bool { return s != PairEdge }

// PairMetrics is the result of a zero-lag comparison over one fixed window.
// Correlation is signed. EnergyRatio is energy(left)/energy(right).
type PairMetrics struct {
	Correlation float64
	EnergyLeft  float64
	EnergyRight float64
	EnergyRatio float64
	MeanLeft    float64
	MeanRight   float64
	RMSLeft     float64
	RMSRight    float64
	SilentLeft  bool
	SilentRight bool
	HasEdge     bool
	Sign        int
	State       PairState
}

// ComparePair computes mean-removed normalized correlation and the energy
// ratio for one fixed window. The two slices must have equal length.
func ComparePair(left, right []float64, cfg Config) (PairMetrics, error) {
	norm, err := cfg.normalized()
	if err != nil {
		return PairMetrics{}, err
	}
	if len(left) != len(right) {
		return PairMetrics{}, errors.New("taskpkg: pair lengths differ")
	}
	if len(left) == 0 {
		return PairMetrics{}, errors.New("taskpkg: empty pair window")
	}
	for i := range left {
		if math.IsNaN(left[i]) || math.IsInf(left[i], 0) ||
			math.IsNaN(right[i]) || math.IsInf(right[i], 0) {
			return PairMetrics{}, fmt.Errorf("taskpkg: non-finite sample at offset %d", i)
		}
	}

	meanL := mean(left)
	meanR := mean(right)
	var energyL, energyR, dot float64
	for i := range left {
		dl := left[i] - meanL
		dr := right[i] - meanR
		energyL += dl * dl
		energyR += dr * dr
		dot += dl * dr
	}

	m := PairMetrics{
		EnergyLeft:  energyL,
		EnergyRight: energyR,
		MeanLeft:    meanL,
		MeanRight:   meanR,
		RMSLeft:     math.Sqrt(energyL / float64(len(left))),
		RMSRight:    math.Sqrt(energyR / float64(len(right))),
		State:       PairWeakCorrelation,
	}
	m.SilentLeft = m.RMSLeft <= norm.SilenceRMS
	m.SilentRight = m.RMSRight <= norm.SilenceRMS
	if m.SilentLeft || m.SilentRight {
		m.State = PairSilent
		return m, nil
	}

	m.EnergyRatio = energyL / energyR
	m.Correlation = clamp(dot/math.Sqrt(energyL*energyR), -1, 1)
	if m.EnergyRatio < norm.MinEnergyRatio || m.EnergyRatio > norm.MaxEnergyRatio {
		m.State = PairEnergyOutOfRange
		return m, nil
	}
	if math.Abs(m.Correlation) < norm.CorrelationThreshold || m.Correlation == 0 {
		// The second condition prevents a zero correlation from acquiring a
		// sign when the caller explicitly configures a zero threshold.
		return m, nil
	}
	m.HasEdge = true
	m.State = PairEdge
	if m.Correlation < 0 {
		m.Sign = -1
	} else {
		m.Sign = 1
	}
	return m, nil
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	// Compensated summation keeps long windows well behaved.
	var sum, compensation float64
	for _, value := range values {
		y := value - compensation
		t := sum + y
		compensation = (t - sum) - y
		sum = t
	}
	return sum / float64(len(values))
}

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
