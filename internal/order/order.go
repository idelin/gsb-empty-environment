// Package order derives the partial order of aligned events from their
// global-time intervals. Events whose intervals can overlap are left
// incomparable; no midpoint-based tie-breaking is ever applied.
package order

import (
	"clockcal/internal/calib"
)

// Event is one aligned event with its possible global-time interval.
type Event struct {
	ID       string
	Channel  string
	Interval calib.Interval
}

// Relation holds the result of aligning a set of events.
type Relation struct {
	// Before[i][j] is true when event i certainly precedes event j for every
	// feasible calibration parameter choice.
	Before [][]bool
}

// definitelyBefore reports whether e certainly precedes f: the latest
// possible global time of e is strictly earlier than the earliest possible
// global time of f. Infinite bounds never establish an ordering.
func definitelyBefore(e, f Event) bool {
	if e.Interval.Hi == nil || f.Interval.Lo == nil {
		return false
	}
	return e.Interval.Hi.Cmp(f.Interval.Lo) < 0
}

// Build computes the full "certainly before" relation.
func Build(events []Event) Relation {
	n := len(events)
	rel := Relation{Before: make([][]bool, n)}
	for i := range rel.Before {
		rel.Before[i] = make([]bool, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && definitelyBefore(events[i], events[j]) {
				rel.Before[i][j] = true
			}
		}
	}
	return rel
}

// TransitiveReduction removes every edge implied by a path through other
// events, keeping the smallest set of order constraints that generates the
// full relation.
func (r Relation) TransitiveReduction() [][2]int {
	n := len(r.Before)
	var edges [][2]int
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if !r.Before[i][j] {
				continue
			}
			redundant := false
			for k := 0; k < n; k++ {
				if k != i && k != j && r.Before[i][k] && r.Before[k][j] {
					redundant = true
					break
				}
			}
			if !redundant {
				edges = append(edges, [2]int{i, j})
			}
		}
	}
	return edges
}

// Incomparable lists the unordered pairs (i < j): events whose relative
// order cannot be decided from the calibration data.
func (r Relation) Incomparable() [][2]int {
	n := len(r.Before)
	var pairs [][2]int
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if !r.Before[i][j] && !r.Before[j][i] {
				pairs = append(pairs, [2]int{i, j})
			}
		}
	}
	return pairs
}
