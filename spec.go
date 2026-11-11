package wafdecision

import "math"

// MaxLeaves is the maximum number of distinct detector leaves supported by
// the compiler. The exhaustive verifier enumerates all 2^MaxLeaves truth
// assignments.
const MaxLeaves = 12

// LeafSpec describes one detector leaf.
//
// Cost is the non-negative integer cost of invoking the detector once.
// Selectivity is the probability that the detector reports true (a match),
// expressed as the closed interval [SelLo, SelHi] with 0 <= SelLo <= SelHi <= 1.
// Interval endpoints model measurement uncertainty; the compiler uses the
// interval midpoint as a deterministic tie-breaker between decision graphs
// with equal worst-case cost, while Verify evaluates expected costs over the
// full interval box.
type LeafSpec struct {
	ID    int
	Cost  uint64
	SelLo float64
	SelHi float64
}

// SelMid returns the midpoint of the selectivity interval.
func (s LeafSpec) SelMid() float64 { return (s.SelLo + s.SelHi) / 2 }

func validateSpecs(specs []LeafSpec) (map[int]int, error) {
	if len(specs) == 0 {
		return nil, ErrNoLeaves
	}
	if len(specs) > MaxLeaves {
		return nil, ErrTooManyLeaves
	}
	byID := make(map[int]int, len(specs))
	for idx, s := range specs {
		if s.SelLo < 0 || s.SelHi < 0 || s.SelLo > 1 || s.SelHi > 1 {
			return nil, &LeafError{LeafID: s.ID, Reason: "selectivity outside [0,1]"}
		}
		if s.SelLo > s.SelHi {
			return nil, &LeafError{LeafID: s.ID, Reason: "SelLo > SelHi"}
		}
		if math.IsNaN(s.SelLo) || math.IsNaN(s.SelHi) {
			return nil, &LeafError{LeafID: s.ID, Reason: "selectivity is NaN"}
		}
		if _, dup := byID[s.ID]; dup {
			return nil, &LeafError{LeafID: s.ID, Reason: "duplicate leaf identifier"}
		}
		byID[s.ID] = idx
	}
	return byID, nil
}
