package tombgc

import (
	"fmt"
	"sync"
)

// GCStats summarises one garbage-collection pass.
type GCStats struct {
	Reclaimed      int   // number of tombstones physically removed
	ReclaimedBytes int64 // bytes freed
	Remaining      int   // tombstones still retained
	RemainingBytes int64 // bytes still retained
}

// Coordinator tracks local tombstones plus the causal frontier advertised by
// every node in the cluster. From these it derives the Causal Stability
// Frontier and physically reclaims any tombstone the whole cluster has
// already observed.
//
// Coordinator is safe for concurrent use.
type Coordinator struct {
	mu sync.Mutex

	self  string
	peers []string

	// tombstones holds the live tombstone set owned by this node.
	tombstones []Tombstone
	// bytes is the total in-memory footprint of tombstones.
	bytes int64

	// frontiers[node] is the latest causal frontier advertised by node,
	// including this node itself.
	frontiers map[string]VersionVector
}

// NewCoordinator creates a coordinator for node self in a cluster made of
// self plus the given peers.
func NewCoordinator(self string, peers ...string) *Coordinator {
	c := &Coordinator{
		self:      self,
		peers:     append([]string(nil), peers...),
		frontiers: make(map[string]VersionVector, len(peers)+1),
	}
	// Seed every known node with an empty frontier so the stability
	// frontier is well-defined (and zero) before any gossip arrives.
	c.frontiers[self] = VersionVector{}
	for _, p := range peers {
		c.frontiers[p] = VersionVector{}
	}
	return c
}

// Self returns the id of the node this coordinator runs on.
func (c *Coordinator) Self() string { return c.self }

// AddTombstone registers a tombstone created by a local delete.
func (c *Coordinator) AddTombstone(t Tombstone) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tombstones = append(c.tombstones, t)
	c.bytes += t.SizeBytes
}

// Observe records that the local node has causally advanced to the given
// frontier (e.g. after applying a batch of remote operations) and returns
// the merged local frontier for gossip.
func (c *Coordinator) Observe(vv VersionVector) VersionVector {
	c.mu.Lock()
	defer c.mu.Unlock()
	local := c.frontiers[c.self]
	local.Merge(vv)
	return local.Copy()
}

// ReceiveFrontier folds a gossiped frontier from a peer into the local view.
func (c *Coordinator) ReceiveFrontier(from string, vv VersionVector) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.frontiers[from]
	if !ok {
		cur = VersionVector{}
		c.peers = append(c.peers, from)
	}
	cur.Merge(vv)
	c.frontiers[from] = cur
}

// LocalFrontier returns the frontier this node advertises to its peers.
func (c *Coordinator) LocalFrontier() VersionVector {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frontiers[c.self].Copy()
}

// StabilityFrontier computes the Causal Stability Frontier: the
// component-wise minimum over the frontiers of every known node. A
// tombstone dominated by this frontier has been observed cluster-wide.
func (c *Coordinator) StabilityFrontier() VersionVector {
	c.mu.Lock()
	defer c.mu.Unlock()
	vectors := make([]VersionVector, 0, len(c.frontiers))
	for _, vv := range c.frontiers {
		vectors = append(vectors, vv)
	}
	return Min(vectors...)
}

// CollectGarbage physically removes every tombstone dominated by the Causal
// Stability Frontier and returns statistics about the pass. Tombstones not
// yet observed by the whole cluster are retained.
func (c *Coordinator) CollectGarbage() GCStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	vectors := make([]VersionVector, 0, len(c.frontiers))
	for _, vv := range c.frontiers {
		vectors = append(vectors, vv)
	}
	frontier := Min(vectors...)

	kept := c.tombstones[:0]
	stats := GCStats{}
	for _, t := range c.tombstones {
		if t.Stable(frontier) {
			stats.Reclaimed++
			stats.ReclaimedBytes += t.SizeBytes
			c.bytes -= t.SizeBytes
			continue
		}
		kept = append(kept, t)
	}
	// Nil out the tail so reclaimed tombstones are not pinned by the
	// backing array and can be freed by the Go runtime GC.
	for i := len(kept); i < len(c.tombstones); i++ {
		c.tombstones[i] = Tombstone{}
	}
	c.tombstones = kept
	stats.Remaining = len(c.tombstones)
	stats.RemainingBytes = c.bytes
	return stats
}

// TombstoneCount returns the number of retained tombstones.
func (c *Coordinator) TombstoneCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tombstones)
}

// TombstoneBytes returns the in-memory footprint of retained tombstones.
func (c *Coordinator) TombstoneBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

func (c *Coordinator) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("node=%s tombstones=%d bytes=%d", c.self, len(c.tombstones), c.bytes)
}
