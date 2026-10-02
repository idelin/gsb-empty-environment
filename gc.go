package main

// Coordinator drives tombstone garbage collection across the cluster once
// the designated coordinator nodes confirm causal stability.
type Coordinator struct {
	cluster      []*Node
	coordinators []*Node
}

// NewCoordinator builds a GC coordinator. Collection only starts when
// every node listed in coordinatorIDs confirms stability from its own
// local view of the cluster clocks.
func NewCoordinator(cluster []*Node, coordinatorIDs ...string) *Coordinator {
	byID := map[string]*Node{}
	for _, n := range cluster {
		byID[n.ID] = n
	}
	c := &Coordinator{cluster: cluster}
	for _, id := range coordinatorIDs {
		if n, ok := byID[id]; ok {
			c.coordinators = append(c.coordinators, n)
		}
	}
	return c
}

// GCStats summarizes one GC pass.
type GCStats struct {
	Stable     bool  // whether collection actually ran
	Reclaimed  int   // tombstones physically removed
	FreedBytes int64 // bytes released by reclamation
	MemBefore  int64 // cluster-wide accounted bytes before GC
	MemAfter   int64 // cluster-wide accounted bytes after GC
}

// ReductionPct returns the memory reduction as a percentage.
func (s GCStats) ReductionPct() float64 {
	if s.MemBefore == 0 {
		return 0
	}
	return 100 * float64(s.MemBefore-s.MemAfter) / float64(s.MemBefore)
}

// RunGC physically collects all tombstones with T < threshold on every
// node, but only after each coordinator node confirms that all cluster
// members have passed T = threshold. Returns stats with Stable=false and
// no reclamation if the frontier has not advanced far enough yet.
func (c *Coordinator) RunGC(threshold uint64) GCStats {
	stats := GCStats{}
	for _, n := range c.cluster {
		stats.MemBefore += n.Store.Memory()
	}

	peers := len(c.cluster)
	for _, coord := range c.coordinators {
		if !coord.StableAt(threshold, peers) {
			return stats // not causally stable: collect nothing
		}
	}
	stats.Stable = true

	// Cluster-wide frontier: component-wise minimum of all store clocks.
	clocks := make([]VectorClock, 0, len(c.cluster))
	for _, n := range c.cluster {
		clocks = append(clocks, n.Store.Clock())
	}
	frontier := Frontier(clocks...)

	for _, n := range c.cluster {
		reclaimed, freed := n.Store.GC(frontier, threshold)
		stats.Reclaimed += reclaimed
		stats.FreedBytes += freed
	}
	for _, n := range c.cluster {
		stats.MemAfter += n.Store.Memory()
	}
	return stats
}
