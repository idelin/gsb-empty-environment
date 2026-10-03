package inspector

import (
	"fmt"
	"strings"
)

// Invariant is a named assertion evaluated against a Report. A nil return
// means the invariant holds.
type Invariant struct {
	Name  string
	Check func(*Report) error
}

// ViolationError aggregates all failed invariants.
type ViolationError struct {
	Violations []string
}

func (e *ViolationError) Error() string {
	return "invariant violations: " + strings.Join(e.Violations, "; ")
}

// Assert evaluates every invariant against the report and returns nil if
// all hold, otherwise a *ViolationError listing each failure.
func Assert(rep *Report, invariants ...Invariant) error {
	v := &ViolationError{}
	for _, inv := range invariants {
		if err := inv.Check(rep); err != nil {
			v.Violations = append(v.Violations, fmt.Sprintf("%s: %v", inv.Name, err))
		}
	}
	if len(v.Violations) > 0 {
		return v
	}
	return nil
}

// NoDanglingRefs asserts the graph contains no dangling references.
var NoDanglingRefs = Invariant{"NoDanglingRefs", func(r *Report) error {
	if n := len(r.IssuesOf(KindDanglingRef)); n > 0 {
		return fmt.Errorf("%d dangling reference(s)", n)
	}
	return nil
}}

// NoOrphanCycles asserts no cycle is unreachable from the roots.
var NoOrphanCycles = Invariant{"NoOrphanCycles", func(r *Report) error {
	if n := len(r.IssuesOf(KindOrphanCycle)); n > 0 {
		return fmt.Errorf("%d orphan cycle(s)", n)
	}
	return nil
}}

// NoDuplicateIDs asserts record IDs are unique.
var NoDuplicateIDs = Invariant{"NoDuplicateIDs", func(r *Report) error {
	if n := len(r.IssuesOf(KindDuplicateID)); n > 0 {
		return fmt.Errorf("%d duplicate id(s)", n)
	}
	return nil
}}

// NoNegativeWeights asserts all weights are non-negative.
var NoNegativeWeights = Invariant{"NoNegativeWeights", func(r *Report) error {
	if n := len(r.IssuesOf(KindNegativeWeight)); n > 0 {
		return fmt.Errorf("%d negative weight(s)", n)
	}
	return nil
}}

// SkewWithin returns an invariant asserting the skew ratio stays within limit.
func SkewWithin(limit float64) Invariant {
	return Invariant{"SkewWithin", func(r *Report) error {
		if r.SkewRatio > limit {
			return fmt.Errorf("skew ratio %.2f exceeds %.2f", r.SkewRatio, limit)
		}
		return nil
	}}
}

// AllReachable asserts every node is reachable from a root.
var AllReachable = Invariant{"AllReachable", func(r *Report) error {
	if r.Reachable != r.Nodes {
		return fmt.Errorf("only %d of %d nodes reachable", r.Reachable, r.Nodes)
	}
	return nil
}}

// StandardInvariants is the default formal constraint bundle.
var StandardInvariants = []Invariant{
	NoDanglingRefs, NoOrphanCycles, NoDuplicateIDs, NoNegativeWeights,
}
