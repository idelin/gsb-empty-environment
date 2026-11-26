package hvsched

import "math/big"

// WeightInterval is an inclusive candidate integer weight range.
type WeightInterval = VarSpec

// Candidate is a named candidate integer weight interval.
type Candidate = VarSpec

// CompressedArrival is a run of equal-length packets.
type CompressedArrival = ArrivalSpec

// EmissionObservation is one observed leaf emission.
type EmissionObservation = Observation

// InferWeights is the domain-language alias for Solve.
func InferWeights(p Problem) (*SolutionSet, error) { return Solve(p) }

// Count returns the exact number of solutions as a decimal string. It is
// useful at JSON/API boundaries where *big.Int is less convenient.
func (s *SolutionSet) CountString() string { return s.Count().String() }

// KthMap returns the one-based lexicographic solution as a name/value map.
func (s *SolutionSet) KthMap(k *big.Int) (map[string]uint64, WeightVector, error) {
	v, err := s.Kth(k)
	if err != nil {
		return nil, nil, err
	}
	m, err := v.Map(s.problem)
	if err != nil {
		return nil, nil, err
	}
	return m, v, nil
}
