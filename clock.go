package main

// Dot identifies a single causal event: (node, counter).
type Dot struct {
	Node  string
	Clock uint64
}

// VectorClock tracks the highest event counter known per node.
type VectorClock map[string]uint64

// Copy returns an independent copy of the clock.
func (vc VectorClock) Copy() VectorClock {
	out := make(VectorClock, len(vc))
	for k, v := range vc {
		out[k] = v
	}
	return out
}

// Merge takes the component-wise maximum with another clock.
func (vc VectorClock) Merge(other VectorClock) {
	for k, v := range other {
		if v > vc[k] {
			vc[k] = v
		}
	}
}

// Dominates reports whether the clock has causally seen the given dot.
func (vc VectorClock) Dominates(d Dot) bool {
	return vc[d.Node] >= d.Clock
}

// Frontier computes the component-wise minimum across the given clocks:
// the causal stability frontier. Every event at or below it is known to
// have been observed by all participants. Missing entries count as 0.
func Frontier(clocks ...VectorClock) VectorClock {
	out := VectorClock{}
	if len(clocks) == 0 {
		return out
	}
	for k, v := range clocks[0] {
		min := v
		for _, c := range clocks[1:] {
			if c[k] < min {
				min = c[k]
			}
		}
		out[k] = min
	}
	return out
}
