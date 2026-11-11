package netsec

import "math/big"

// EdgeFlow is one step of the canonical one-dimensional transport plan: the
// signed amount of probability mass carried across the boundary between
// adjacent domain positions From and From+1. A positive flow moves mass from
// From toward From+1 (rightward); a negative flow moves it leftward. The value
// equals the cumulative distribution difference
// sum_{j<=From}(groupProb[j] - populationProb[j]) and is kept as an exact
// rational. Each unit crossing one boundary contributes |Flow| to the total
// transport cost.
type EdgeFlow struct {
	From int
	Flow *big.Rat
}

// Cost is the contribution |Flow| * 1 of this edge to the earth-mover distance.
func (f EdgeFlow) Cost() *big.Rat {
	return new(big.Rat).Abs(f.Flow)
}

// TransportFlows returns the canonical edge flows that transform the group
// distribution into the population distribution on an ordered domain. Both
// inputs must describe probability distributions of equal length whose entries
// sum to one; callers derive them with GroupDistribution /
// PopulationDistribution.
func TransportFlows(groupDist, populationDist []*big.Rat) ([]EdgeFlow, error) {
	if len(groupDist) != len(populationDist) {
		return nil, errLengthMismatch(len(groupDist), len(populationDist))
	}
	for i := range groupDist {
		if groupDist[i] == nil || populationDist[i] == nil {
			return nil, errNilProbability(i)
		}
	}
	flows := make([]EdgeFlow, 0, len(groupDist)-1)
	carry := new(big.Rat)
	for i := 0; i+1 < len(groupDist); i++ {
		carry.Add(carry, new(big.Rat).Sub(groupDist[i], populationDist[i]))
		flows = append(flows, EdgeFlow{From: i, Flow: new(big.Rat).Set(carry)})
	}
	return flows, nil
}

// Wasserstein1 is the exact one-dimensional earth-mover (Wasserstein-1)
// distance between two probability distributions over the ordered domain,
// using unit ground distance between adjacent positions. It equals the sum of
// absolute canonical edge flows.
func Wasserstein1(groupDist, populationDist []*big.Rat) (*big.Rat, error) {
	flows, err := TransportFlows(groupDist, populationDist)
	if err != nil {
		return nil, err
	}
	distance := new(big.Rat)
	for _, flow := range flows {
		distance.Add(distance, flow.Cost())
	}
	return distance, nil
}

// CountWasserstein1 computes the exact normalized transport distance directly
// from raw integer counts: groupCounts/groupTotal versus
// populationCounts/populationTotal. It is the count-level equivalent of
// Wasserstein1 used by the verifier and tests.
func CountWasserstein1(groupCounts []int64, groupTotal int64, populationCounts []int64, populationTotal int64) (*big.Rat, error) {
	if len(groupCounts) != len(populationCounts) {
		return nil, errLengthMismatch(len(groupCounts), len(populationCounts))
	}
	if groupTotal <= 0 {
		return nil, errNonPositive("group", groupTotal)
	}
	if populationTotal <= 0 {
		return nil, errNonPositive("population", populationTotal)
	}

	groupDenom := big.NewInt(groupTotal)
	popDenom := big.NewInt(populationTotal)
	distance := new(big.Rat)
	carry := new(big.Rat)
	for i := 0; i+1 < len(groupCounts); i++ {
		groupProb := new(big.Rat).SetFrac(big.NewInt(groupCounts[i]), new(big.Int).Set(groupDenom))
		popProb := new(big.Rat).SetFrac(big.NewInt(populationCounts[i]), new(big.Int).Set(popDenom))
		carry.Add(carry, new(big.Rat).Sub(groupProb, popProb))
		distance.Add(distance, new(big.Rat).Abs(carry))
	}
	return distance, nil
}
