package gossip

import "math/rand"

// Cluster is an in-memory network of nodes used to simulate gossip
// rounds ("periods") without real sockets.
type Cluster struct {
	Nodes []*Node
	rnd   *rand.Rand
}

// NewCluster builds n nodes that all know each other (a converged view).
func NewCluster(n int, seed int64) *Cluster {
	c := &Cluster{rnd: rand.New(rand.NewSource(seed))}
	for i := 0; i < n; i++ {
		c.Nodes = append(c.Nodes, NewNode(nodeID(i), n+1))
	}
	for _, a := range c.Nodes {
		for _, b := range c.Nodes {
			if a != b {
				a.members[b.ID] = Member{ID: b.ID, State: Alive}
			}
		}
	}
	return c
}

func nodeID(i int) string {
	return "node-" + itoa(i)
}

// itoa avoids importing strconv just for the simulation helper.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// AddNode creates a fresh node that joins the cluster via a random
// introducer. Only the introducer knows about it afterwards.
func (c *Cluster) AddNode(id string) *Node {
	n := NewNode(id, len(c.Nodes)+1)
	introducer := c.Nodes[c.rnd.Intn(len(c.Nodes))]
	n.Join(introducer)
	c.Nodes = append(c.Nodes, n)
	return n
}

// RunPeriod executes one gossip period: every node pings one random peer,
// exchanging piggybacked events in both directions (Ping and Ack).
func (c *Cluster) RunPeriod() {
	for _, n := range c.Nodes {
		target := c.Nodes[c.rnd.Intn(len(c.Nodes))]
		if target == n {
			continue
		}
		n.Ping(target)
	}
}

// AllKnow reports whether every node in the cluster has id in its view.
func (c *Cluster) AllKnow(id string) bool {
	for _, n := range c.Nodes {
		if !n.Knows(id) {
			return false
		}
	}
	return true
}

// AwareCount reports how many nodes have id in their view table.
func (c *Cluster) AwareCount(id string) int {
	count := 0
	for _, n := range c.Nodes {
		if n.Knows(id) {
			count++
		}
	}
	return count
}
