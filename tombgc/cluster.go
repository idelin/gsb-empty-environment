package tombgc

import "fmt"

// Cluster is an in-memory simulation of a set of CRDT nodes that gossip
// their causal frontiers to one another. It exists to exercise and
// demonstrate the Coordinator; a production deployment would swap GossipAll
// for a real anti-entropy transport.
type Cluster struct {
	Nodes map[string]*Coordinator
	ids   []string
}

// NewCluster builds a fully-connected simulated cluster with the given node
// ids.
func NewCluster(ids ...string) *Cluster {
	cl := &Cluster{Nodes: make(map[string]*Coordinator, len(ids)), ids: append([]string(nil), ids...)}
	for _, id := range ids {
		peers := make([]string, 0, len(ids)-1)
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		cl.Nodes[id] = NewCoordinator(id, peers...)
	}
	return cl
}

// IDs returns the node ids in the order they were registered.
func (cl *Cluster) IDs() []string { return append([]string(nil), cl.ids...) }

// Advance moves node id's causal frontier to Lamport time t for every node
// it knows about (a stand-in for having applied all operations up to t).
func (cl *Cluster) Advance(id string, t uint64) {
	vv := VersionVector{}
	for _, n := range cl.ids {
		vv[n] = t
	}
	cl.Nodes[id].Observe(vv)
}

// GossipAll performs one synchronous round of frontier gossip: every node
// sends its local frontier to every other node.
func (cl *Cluster) GossipAll() {
	for _, from := range cl.ids {
		frontier := cl.Nodes[from].LocalFrontier()
		for _, to := range cl.ids {
			if to != from {
				cl.Nodes[to].ReceiveFrontier(from, frontier)
			}
		}
	}
}

// TotalTombstoneBytes sums the retained tombstone bytes across the cluster.
func (cl *Cluster) TotalTombstoneBytes() int64 {
	var total int64
	for _, n := range cl.Nodes {
		total += n.TombstoneBytes()
	}
	return total
}

// CollectGarbage runs a GC pass on every node and returns per-node stats.
func (cl *Cluster) CollectGarbage() map[string]GCStats {
	out := make(map[string]GCStats, len(cl.Nodes))
	for id, n := range cl.Nodes {
		out[id] = n.CollectGarbage()
	}
	return out
}

func (cl *Cluster) String() string {
	s := ""
	for _, id := range cl.ids {
		s += fmt.Sprintf("  %s\n", cl.Nodes[id])
	}
	return s
}
