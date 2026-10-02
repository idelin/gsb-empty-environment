// Package tombgc implements a Tombstone Garbage Collection coordinator for
// long-running CRDT clusters. It prunes causal history by computing a
// distributed Causal Stability Frontier: a tombstone becomes physically
// reclaimable once every node in the cluster has causally observed it.
package tombgc

import "sort"

// VersionVector is a causal frontier: the maximum Lamport timestamp observed
// from each node in the cluster.
type VersionVector map[string]uint64

// Copy returns a deep copy of the vector.
func (v VersionVector) Copy() VersionVector {
	out := make(VersionVector, len(v))
	for node, ts := range v {
		out[node] = ts
	}
	return out
}

// Merge folds other into v, keeping the component-wise maximum.
func (v VersionVector) Merge(other VersionVector) {
	for node, ts := range other {
		if ts > v[node] {
			v[node] = ts
		}
	}
}

// Dominates reports whether every entry of other is covered by v, i.e. every
// event in other's causal history is already known to v.
func (v VersionVector) Dominates(other VersionVector) bool {
	for node, ts := range other {
		if v[node] < ts {
			return false
		}
	}
	return true
}

// Min returns the component-wise minimum of all vectors. Only nodes present
// in every vector contribute; this is the Causal Stability Frontier.
func Min(vectors ...VersionVector) VersionVector {
	if len(vectors) == 0 {
		return VersionVector{}
	}
	out := vectors[0].Copy()
	for node, ts := range out {
		for _, other := range vectors[1:] {
			ots, ok := other[node]
			if !ok || ots < ts {
				if !ok {
					delete(out, node)
				} else {
					out[node] = ots
				}
				break
			}
		}
	}
	return out
}

// Nodes returns the sorted node ids present in the vector.
func (v VersionVector) Nodes() []string {
	nodes := make([]string, 0, len(v))
	for node := range v {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	return nodes
}
