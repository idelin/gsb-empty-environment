package hvsched

import (
	"fmt"
	"math/big"
)

// MaxSupportedWeight is the inclusive upper bound anticipated by the
// diagnostic contract. Domains may also use smaller bounds.
const MaxSupportedWeight uint64 = 1 << 32

// VarSpec describes one free integer weight. A fixed weight is represented by
// setting Min == Max.
type VarSpec struct {
	// Name is used in diagnostics and WeightMap.
	Name string
	// Min and Max are inclusive integer bounds.
	Min uint64
	Max uint64
	// Layer is the key of the layer total in Problem.LayerSums.
	// An empty layer is unconstrained by a total.
	Layer string
}

// NodeSpec is one scheduler or leaf queue. The unique node with an empty
// Parent is the root scheduler. Nodes without children are leaf queues.
type NodeSpec struct {
	ID     string
	Parent string
	// Layer identifies the layer total in Problem.LayerSums.
	Layer string
	// Tie is the tenant number used for exact virtual-time ties.
	Tie uint64

	// WeightVar names the VarSpec supplying this non-root node's weight.
	WeightVar string
	// FixedWeight is used when WeightVar is empty.
	FixedWeight uint64
}

// ArrivalSpec is a compressed run of Count equal-length packets arriving
// before emission index Before. Before uses zero-based observation indices;
// zero means initially queued.
type ArrivalSpec struct {
	Leaf   string
	Before int
	Count  uint64
	Length uint64
}

// Observation is one real emission. Leaf is the emitted leaf identifier.
//
// Finish, when non-nil, is an optional observed exact virtual completion tag
// at FinishScheduler. If FinishScheduler is empty, the tag at the leaf's
// direct parent scheduler is checked.
type Observation struct {
	Leaf              string
	Finish            *big.Rat
	PartialCompletion *big.Rat
	// FinishNode identifies the child edge whose parent-level finish tag
	// is supplied. Empty means the observed leaf itself.
	FinishNode string
}

func (o Observation) completionConstraint() *big.Rat {
	if o.Finish != nil {
		return o.Finish
	}
	return o.PartialCompletion
}

// ClockMode selects how a scheduler's parent virtual clock is advanced after a
// selected packet. StartTag is the SFQ-like max-reset interpretation;
// FinishTag advances the clock to the selected virtual finish tag.
type ClockMode int

const (
	// ClockFinishTag sets parent virtual time to the selected finish tag.
	// It is the zero/default mode and matches a completion-time clock.
	ClockFinishTag ClockMode = iota
	// ClockStartTag sets parent virtual time to the selected packet's
	// start tag, i.e. max(parentTime, localLastFinish). This is an
	// SFQ-like reset clock.
	ClockStartTag
)

// Problem is the complete diagnostic input.
type Problem struct {
	Vars         []VarSpec
	LayerSums    map[string]uint64
	Nodes        []NodeSpec
	Arrivals     []ArrivalSpec
	Observations []Observation
	Clock        ClockMode
}

// Region is a closed inclusive box of candidate variable values. Every
// layer-sum-valid point in a reported Region reproduces the observation
// prefix that was solved.
type Region struct {
	Lo []uint64
	Hi []uint64
}

// WeightVector is one lexicographically ordered variable assignment.
type WeightVector []uint64

// Map renders a vector using variable names.
func (v WeightVector) Map(p Problem) (map[string]uint64, error) {
	if len(v) != len(p.Vars) {
		return nil, fmt.Errorf("hvsched: vector length %d does not match %d variables", len(v), len(p.Vars))
	}
	out := make(map[string]uint64, len(v))
	for i, spec := range p.Vars {
		out[spec.Name] = v[i]
	}
	return out, nil
}

// MustMap is Map for contexts where the problem is known to match.
func (v WeightVector) MustMap(p Problem) map[string]uint64 {
	m, err := v.Map(p)
	if err != nil {
		panic(err)
	}
	return m
}

// Emission is one forward-replay decision.
type Emission struct {
	Leaf string
	// Finish is the observed leaf's finish tag at its direct parent.
	Finish *big.Rat
	// Finishes contains the finish tag charged for every edge on the path.
	Finishes map[string]*big.Rat
	// SchedulerTimes contains the advanced parent virtual clock selected
	// for every scheduler on the emitted path.
	SchedulerTimes map[string]*big.Rat
}

// ReplayResult contains a concrete forward replay.
type ReplayResult struct {
	Emissions []Emission
}

// SolutionSet is the compact result of inference.
type SolutionSet struct {
	problem         Problem
	compiled        *compiled
	regions         []Region
	total           *big.Int
	badPrefixLength int
}

// Problem returns a copy of the solved problem.
func (s *SolutionSet) Problem() Problem { return s.problem }

// Regions returns the disjoint feasible boxes found by the solver.
func (s *SolutionSet) Regions() []Region { return s.regions }

// Count returns the exact number of feasible integer weight vectors.
func (s *SolutionSet) Count() *big.Int {
	if s == nil || s.total == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(s.total)
}

// BadPrefixLength is the shortest observation-prefix length that excludes
// every candidate. It is -1 when the complete observation is feasible.
func (s *SolutionSet) BadPrefixLength() int { return s.badPrefixLength }

// ContradictionIndex returns the zero-based index of the first observation
// that makes the prefix impossible. It is -1 when no contradiction exists.
func (s *SolutionSet) ContradictionIndex() int {
	if s.badPrefixLength <= 0 {
		return -1
	}
	return s.badPrefixLength - 1
}
