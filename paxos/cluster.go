package paxos

// Cluster bundles a Network with its Nodes for tests and demos. The
// transport is synchronous, so no background goroutines exist and Shutdown
// is trivially leak-free.
type Cluster struct {
	Net   *Network
	Nodes []*Node
}

// NewCluster creates a fully-connected cluster of size nodes (ids 0..size-1).
func NewCluster(size int) *Cluster {
	net := NewNetwork()
	peers := make([]int, size)
	for i := range peers {
		peers[i] = i
	}
	c := &Cluster{Net: net}
	for i := 0; i < size; i++ {
		c.Nodes = append(c.Nodes, NewNode(i, peers, net, NewMetrics()))
	}
	return c
}

// Leader returns the first node currently holding leadership, or nil.
func (c *Cluster) Leader() *Node {
	for _, n := range c.Nodes {
		if n.Status().IsLeader {
			return n
		}
	}
	return nil
}

// Snapshots collects a metrics snapshot from every node.
func (c *Cluster) Snapshots() []Snapshot {
	snaps := make([]Snapshot, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		snaps = append(snaps, n.Metrics().Snapshot(n.ID()))
	}
	return snaps
}

// Aggregate returns the cluster-wide aggregated metrics.
func (c *Cluster) Aggregate() Aggregate {
	return AggregateSnapshots(c.Snapshots()...)
}

// Shutdown releases cluster resources. It is a no-op by design (synchronous
// transport, no timers), and exists so callers can assert leak-freedom.
func (c *Cluster) Shutdown() {}
