package prioritylb

import (
	"math"
	"time"
)

const epsilon = 1e-12

type groupStats struct {
	total    int
	healthy  int
	degraded int
}

// Calculate computes priority traffic shares and per-endpoint probabilities.
//
// Groups must be ordered P0..Pn. The returned error is non-nil only for
// invalid configuration or when no endpoint can receive traffic.
func Calculate(groups []PriorityGroup, cfg Config) (*Result, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}

	stats := make([]groupStats, len(groups))
	totalEndpoints := 0
	for g, group := range groups {
		st := groupStats{total: len(group.Endpoints)}
		for _, ep := range group.Endpoints {
			if math.IsNaN(ep.Weight) || ep.Weight < 0 {
				return nil, errInvalid("endpoint weight must be non-negative and finite")
			}
			switch ep.State {
			case Healthy:
				st.healthy++
			case Degraded:
				st.degraded++
			case Unhealthy:
			default:
				return nil, errInvalid("unknown endpoint health state")
			}
		}
		stats[g] = st
		totalEndpoints += st.total
	}
	if totalEndpoints == 0 {
		return nil, ErrNoEndpoints
	}

	factor := cfg.OverprovisioningFactor
	if factor <= 0 {
		factor = 1
	}

	availability := make([][2]float64, len(groups))
	healthyFraction := make([]float64, len(groups))
	for g, st := range stats {
		if st.total == 0 {
			continue
		}
		healthyFraction[g] = float64(st.healthy) / float64(st.total)
		availability[g][0] = cap1(float64(st.healthy) / float64(st.total) * factor)
		availability[g][1] = cap1(float64(st.degraded) / float64(st.total) * factor)
	}

	panic := shouldPanic(stats, healthyFraction, cfg.PanicThreshold)

	var shares []float64
	if panic {
		shares = make([]float64, len(groups))
		for g, st := range stats {
			shares[g] = float64(st.total) / float64(totalEndpoints)
		}
	} else {
		shares = normalShares(stats, availability)
	}
	shares = normalizeVector(shares)

	result := &Result{
		Panic:          panic,
		PriorityShares: make([]PriorityShare, len(groups)),
		Endpoints:      make([]EndpointProbability, 0, totalEndpoints),
	}
	for g := range groups {
		result.PriorityShares[g] = PriorityShare{
			Priority:             g,
			Share:                shares[g],
			HealthyAvailability:  availability[g][0],
			DegradedAvailability: availability[g][1],
		}
	}

	for g, group := range groups {
		if shares[g] <= epsilon || len(group.Endpoints) == 0 {
			for i, ep := range group.Endpoints {
				result.Endpoints = append(result.Endpoints, EndpointProbability{
					Priority:      g,
					EndpointIndex: i,
					EndpointID:    ep.ID,
				})
			}
			continue
		}

		weights := make([]float64, len(group.Endpoints))
		weightSum := 0.0
		for i, ep := range group.Endpoints {
			if !panic && ep.State == Unhealthy {
				continue
			}
			ramp := slowStartMultiplier(cfg.Now.Sub(ep.OnlineAt), cfg)
			eff := ep.Weight * ramp
			weights[i] = eff
			weightSum += eff
		}
		if weightSum <= epsilon {
			return nil, ErrNoEndpoints
		}

		probs := make([]float64, len(weights))
		largest := 0
		groupProbSum := 0.0
		for i, w := range weights {
			probs[i] = shares[g] * w / weightSum
			groupProbSum += probs[i]
			if weights[i] > weights[largest] {
				largest = i
			}
		}
		// Correct the floating-point residual against this group's assigned
		// share without renormalizing the vector to 1 (which would erase the
		// priority share).
		probs[largest] += shares[g] - groupProbSum

		for i, ep := range group.Endpoints {
			result.Endpoints = append(result.Endpoints, EndpointProbability{
				Priority:        g,
				EndpointIndex:   i,
				EndpointID:      ep.ID,
				EffectiveWeight: weights[i],
				Probability:     probs[i],
			})
		}
	}

	return result, nil
}

// normalShares implements the Envoy two-pass priority assignment: healthy
// capacity of P0 is consumed first, then its degraded capacity, and only the
// leftover budget carries over to P1 and beyond (Envoy's
// calculatePerPriorityLoad / tryFillLocal structure). When the combined
// effective capacity stays below 100%, every priority's availability is
// divided by the total availability so the shares sum to 1.
func normalShares(stats []groupStats, availability [][2]float64) []float64 {
	n := len(stats)
	healthyLoad := make([]float64, n)
	degradedLoad := make([]float64, n)

	remaining := 1.0
	for g := range stats {
		if stats[g].total == 0 || remaining <= epsilon {
			continue
		}
		// First pass inside this priority: its healthy capacity.
		take := math.Min(remaining, availability[g][0])
		healthyLoad[g] = take
		remaining -= take
		if remaining <= epsilon {
			continue
		}
		// Second pass inside the same priority: its degraded capacity.
		take = math.Min(remaining, availability[g][1])
		degradedLoad[g] = take
		remaining -= take
	}

	shares := make([]float64, n)
	if remaining <= epsilon {
		for g := range stats {
			shares[g] = healthyLoad[g] + degradedLoad[g]
		}
		return shares
	}

	// Combined healthy + degraded capacity is still below 100%: scale every
	// priority's effective availability up proportionally.
	effectiveTotal := 0.0
	for g := range stats {
		effectiveTotal += availability[g][0] + availability[g][1]
	}
	if effectiveTotal <= epsilon {
		return shares
	}
	scale := 1.0 / effectiveTotal
	for g := range stats {
		shares[g] = (availability[g][0] + availability[g][1]) * scale
	}
	return shares
}

// shouldPanic reports global panic mode: every non-empty priority must have a
// raw healthy fraction strictly below the threshold. Empty groups are ignored.
func shouldPanic(stats []groupStats, healthyFraction []float64, threshold float64) bool {
	sawNonEmpty := false
	for g, st := range stats {
		if st.total == 0 {
			continue
		}
		sawNonEmpty = true
		if healthyFraction[g] >= threshold {
			return false
		}
	}
	return sawNonEmpty
}

// slowStartMultiplier returns max(m, (age/W)^(1/A)) for ages inside the
// window. A negative age (OnlineAt in the future) clamps the progress at zero,
// so the endpoint simply receives the floor instead of an undefined power.
func slowStartMultiplier(age time.Duration, cfg Config) float64 {
	if cfg.SlowStartWindow <= 0 || age < 0 || age >= cfg.SlowStartWindow {
		return 1
	}
	progress := float64(age) / float64(cfg.SlowStartWindow)
	if progress < 0 {
		progress = 0
	}
	ramp := math.Pow(progress, 1.0/cfg.AggressionExponent)
	return math.Max(cfg.MinWeightFloor, ramp)
}

func cap1(v float64) float64 {
	if v > 1 {
		return 1
	}
	return v
}

// normalizeVector scales v so it sums to 1 and puts the rounding residual on
// the largest element, guaranteeing the returned vector sums to exactly 1.
func normalizeVector(v []float64) []float64 {
	sum := 0.0
	largest := -1
	for i, x := range v {
		sum += x
		if largest == -1 || x > v[largest] {
			largest = i
		}
	}
	if sum <= epsilon || largest == -1 {
		return v
	}
	out := make([]float64, len(v))
	normalizedSum := 0.0
	for i, x := range v {
		out[i] = x / sum
		normalizedSum += out[i]
	}
	out[largest] += 1.0 - normalizedSum
	return out
}

type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }

func errInvalid(msg string) error { return validationError{msg: "prioritylb: " + msg} }

func validate(cfg Config) error {
	if math.IsNaN(cfg.OverprovisioningFactor) || math.IsInf(cfg.OverprovisioningFactor, 0) {
		return errInvalid("overprovisioning factor must be finite")
	}
	if cfg.PanicThreshold < 0 || cfg.PanicThreshold > 1 {
		return errInvalid("panic threshold must be in [0, 1]")
	}
	if cfg.AggressionExponent <= 0 {
		return errInvalid("aggression exponent must be > 0")
	}
	if math.IsNaN(cfg.MinWeightFloor) || cfg.MinWeightFloor < 0 || cfg.MinWeightFloor > 1 {
		return errInvalid("min weight floor must be in [0, 1]")
	}
	return nil
}
