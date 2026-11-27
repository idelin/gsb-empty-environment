package estimator

import "math"

// ResourceName identifies a controllable resource, mirroring the VPA API.
type ResourceName string

const (
	// ResourceCPU represents CPU, measured in cores (fractions allowed).
	ResourceCPU ResourceName = "cpu"
	// ResourceMemory represents memory, measured in bytes.
	ResourceMemory ResourceName = "memory"
)

// ContainerResourcePolicy is the subset of the VPA
// ContainerResourcePolicy used by the recommendation engine. A zero
// MinAllowed/MaxAllowed entry means "no bound".
type ContainerResourcePolicy struct {
	// MinAllowed caps recommendations from below per resource.
	MinAllowed map[ResourceName]float64
	// MaxAllowed caps recommendations from above per resource.
	MaxAllowed map[ResourceName]float64
	// ControlledResources lists the resources the policy is allowed to
	// change. An empty list means all known resources are controlled.
	ControlledResources []ResourceName
}

// IsControlled reports whether the policy may act on resource.
func (p ContainerResourcePolicy) IsControlled(resource ResourceName) bool {
	if len(p.ControlledResources) == 0 {
		return true
	}
	for _, controlled := range p.ControlledResources {
		if controlled == resource {
			return true
		}
	}
	return false
}

func clamp(value, minAllowed, maxAllowed float64) float64 {
	if minAllowed > 0 && value < minAllowed {
		value = minAllowed
	}
	if maxAllowed > 0 && value > maxAllowed {
		value = maxAllowed
	}
	return value
}

// ApplyPolicy clamps an Estimate to the policy bounds. UncappedTarget is
// deliberately left untouched so monitoring can surface how far the raw
// recommendation sits outside the allowed range. When a resource is not
// controlled the estimate is returned unchanged.
//
// After clamping, LowerBound <= Target <= UpperBound is re-established.
func ApplyPolicy(estimate Estimate, resource ResourceName, policy ContainerResourcePolicy) Estimate {
	if !policy.IsControlled(resource) {
		return estimate
	}
	minAllowed := policy.MinAllowed[resource]
	maxAllowed := policy.MaxAllowed[resource]

	result := Estimate{
		UncappedTarget: estimate.UncappedTarget,
		Target:         clamp(estimate.Target, minAllowed, maxAllowed),
		LowerBound:     clamp(estimate.LowerBound, minAllowed, maxAllowed),
		UpperBound:     clamp(estimate.UpperBound, minAllowed, maxAllowed),
	}

	// Re-establish a consistent ordering if both bounds were forced onto the
	// same cap.
	if result.LowerBound > result.Target {
		result.LowerBound = result.Target
	}
	if result.UpperBound < result.Target {
		result.UpperBound = result.Target
	}
	if result.LowerBound > result.UpperBound {
		result.LowerBound = result.UpperBound
	}
	return result
}

// RoundToInteger rounds every field of the estimate to the nearest whole unit,
// used for resources such as memory that must be expressed as integer bytes.
func (e Estimate) RoundToInteger() Estimate {
	return Estimate{
		LowerBound:     mathRound(e.LowerBound),
		Target:         mathRound(e.Target),
		UncappedTarget: e.UncappedTarget,
		UpperBound:     mathRound(e.UpperBound),
	}
}

func mathRound(value float64) float64 {
	if value < 0 {
		return 0
	}
	return math.Floor(value + 0.5)
}
