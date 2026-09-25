package causal

// NodeID identifies a member of the fixed node set.
type NodeID string

// VectorClock records, per node, the sequence number of the latest
// message known to causally precede the current state.
type VectorClock map[NodeID]uint64

// Clone returns a deep copy of the clock.
func (vc VectorClock) Clone() VectorClock {
	out := make(VectorClock, len(vc))
	for k, v := range vc {
		out[k] = v
	}
	return out
}

// merge takes the component-wise maximum of other into vc.
func (vc VectorClock) merge(other VectorClock) {
	for k, v := range other {
		if vc[k] < v {
			vc[k] = v
		}
	}
}
