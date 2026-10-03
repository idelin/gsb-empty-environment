// Package delta implements a delta-state CRDT with incremental,
// watermark-based synchronization.
//
// Instead of shipping the full state on every sync round, each node
// records the causal context (version vector) it has acked from every
// peer, and only ships the operations ("dots") that are not yet
// dominated by that watermark. A sync against an empty watermark is
// exactly a full-state transfer, so both share one code path.
package delta

// ReplicaID uniquely identifies a replica (node) in the system.
type ReplicaID string

// Dot is the unique, immutable identifier of a single operation:
// the producing replica plus its per-replica sequence number.
type Dot struct {
	Replica ReplicaID
	Seq     uint64
}

// IsZero reports whether d is the zero dot, used as the sentinel
// parent of the document head.
func (d Dot) IsZero() bool { return d.Replica == "" && d.Seq == 0 }

// VersionVector is a compact causal context. Because dots from one
// replica are contiguous, a single per-replica watermark summarizes
// arbitrarily many operations, keeping deltas O(#replicas) instead of
// O(#operations).
type VersionVector map[ReplicaID]uint64

// Dominates reports whether the dot is already covered by the vector.
func (vv VersionVector) Dominates(d Dot) bool {
	if d.IsZero() {
		return true
	}
	return vv[d.Replica] >= d.Seq
}

// Join returns the point-wise maximum of both vectors.
func (vv VersionVector) Join(other VersionVector) VersionVector {
	out := vv.Clone()
	for r, s := range other {
		if s > out[r] {
			out[r] = s
		}
	}
	return out
}

// Clone returns a deep copy of the vector.
func (vv VersionVector) Clone() VersionVector {
	out := make(VersionVector, len(vv))
	for r, s := range vv {
		out[r] = s
	}
	return out
}

// Equal reports whether both vectors cover exactly the same dots.
func (vv VersionVector) Equal(other VersionVector) bool {
	if len(vv) != len(other) {
		return false
	}
	for r, s := range vv {
		if other[r] != s {
			return false
		}
	}
	return true
}
