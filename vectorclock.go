package causalrouter

// VectorClock maps node IDs to the latest event sequence number known
// to have occurred at that node.
type VectorClock map[string]uint64

// Copy returns a deep copy of the clock.
func (vc VectorClock) Copy() VectorClock {
	out := make(VectorClock, len(vc))
	for node, seq := range vc {
		out[node] = seq
	}
	return out
}

// Increment advances the clock's entry for node by one.
func (vc VectorClock) Increment(node string) {
	vc[node]++
}

// Merge folds other into vc by taking the element-wise maximum.
func (vc VectorClock) Merge(other VectorClock) {
	for node, seq := range other {
		if seq > vc[node] {
			vc[node] = seq
		}
	}
}

// LessOrEqual reports whether vc is element-wise <= other.
func (vc VectorClock) LessOrEqual(other VectorClock) bool {
	for node, seq := range vc {
		if seq > other[node] {
			return false
		}
	}
	return true
}

// HappenedBefore reports whether vc strictly happened before other,
// i.e. vc <= other and vc != other.
func (vc VectorClock) HappenedBefore(other VectorClock) bool {
	strict := false
	nodes := map[string]bool{}
	for node := range vc {
		nodes[node] = true
	}
	for node := range other {
		nodes[node] = true
	}
	for node := range nodes {
		a, b := vc[node], other[node]
		if a > b {
			return false
		}
		if a < b {
			strict = true
		}
	}
	return strict
}
