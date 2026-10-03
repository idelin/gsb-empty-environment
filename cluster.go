package gsb

import "math/rand"

// Cluster simulates a group of nodes plus a network that can be
// partitioned and healed.
type Cluster struct {
	Nodes       []*Node
	group       map[string]int
	partitioned bool
}

func NewCluster(ids ...string) *Cluster {
	c := &Cluster{group: make(map[string]int)}
	for _, id := range ids {
		c.Nodes = append(c.Nodes, NewNode(id))
	}
	return c
}

// Partition splits the network: nodes are only reachable from nodes whose
// group function returns the same value.
func (c *Cluster) Partition(groupOf func(id string) int) {
	for _, n := range c.Nodes {
		c.group[n.ID] = groupOf(n.ID)
	}
	c.partitioned = true
}

// Heal restores full connectivity.
func (c *Cluster) Heal() { c.partitioned = false }

// Reachable reports whether probes may flow between two nodes right now.
func (c *Cluster) Reachable(a, b *Node) bool {
	return !c.partitioned || c.group[a.ID] == c.group[b.ID]
}

// Round models one probe period: every node picks one random reachable
// peer and runs a digest exchange. It returns how many exchanges found a
// divergence and reconciled it.
func (c *Cluster) Round(rng *rand.Rand) int {
	divergent := 0
	for _, n := range c.Nodes {
		var peers []*Node
		for _, p := range c.Nodes {
			if p != n && c.Reachable(n, p) {
				peers = append(peers, p)
			}
		}
		if len(peers) == 0 {
			continue
		}
		if Sync(n, peers[rng.Intn(len(peers))]) {
			divergent++
		}
	}
	return divergent
}

// Converged reports whether every node holds an identical membership view.
func (c *Cluster) Converged() bool {
	if len(c.Nodes) == 0 {
		return true
	}
	ref := c.Nodes[0].Members()
	sum := c.Nodes[0].DigestSum()
	for _, n := range c.Nodes[1:] {
		if n.DigestSum() != sum {
			return false
		}
		ms := n.Members()
		if len(ms) != len(ref) {
			return false
		}
		for i := range ref {
			if ms[i] != ref[i] {
				return false
			}
		}
	}
	return true
}
