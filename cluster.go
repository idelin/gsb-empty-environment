package gracefulleave

import "time"

// Cluster wires a set of nodes together over one transport and gives each
// node a membership view of its peers. It is a convenience for demos and
// tests; production code would populate membership via gossip/discovery.
type Cluster struct {
	Transport *Transport
	Nodes     []*Node
}

// NewCluster creates one node per ID with the given quiescent period and
// handler, then cross-populates every node's routing table with its peers.
func NewCluster(ids []string, quiescent time.Duration, h Handler) *Cluster {
	t := NewTransport()
	c := &Cluster{Transport: t}
	for _, id := range ids {
		c.Nodes = append(c.Nodes, NewNode(id, "addr-"+id, t, quiescent, h))
	}
	for _, n := range c.Nodes {
		for _, p := range c.Nodes {
			if p.ID != n.ID {
				n.Membership().Add(NewMember(p.ID, p.Addr))
			}
		}
	}
	return c
}

// Node returns the node with the given ID, or nil.
func (c *Cluster) Node(id string) *Node {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}
