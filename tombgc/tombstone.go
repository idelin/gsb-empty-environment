package tombgc

// Tombstone is the deletion marker a CRDT keeps so that deletes can be
// propagated and reconciled. Until every node has causally observed the
// delete, the tombstone must be retained in memory.
type Tombstone struct {
	// Key is the identifier of the deleted CRDT object.
	Key string
	// Version is the causal timestamp (vector clock snapshot) at which the
	// delete was issued.
	Version VersionVector
	// SizeBytes is the in-memory footprint attributed to this tombstone,
	// used for memory accounting and GC reporting.
	SizeBytes int64
}

// Lamport returns the maximum component of the tombstone's causal timestamp,
// useful for logging and bucketed simulations.
func (t Tombstone) Lamport() uint64 {
	var max uint64
	for _, ts := range t.Version {
		if ts > max {
			max = ts
		}
	}
	return max
}

// Stable reports whether the tombstone is causally dominated by the given
// stability frontier, i.e. every node in the cluster has already observed
// this delete and the tombstone is safe to physically reclaim.
func (t Tombstone) Stable(frontier VersionVector) bool {
	return frontier.Dominates(t.Version)
}
