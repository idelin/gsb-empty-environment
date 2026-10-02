package gossip

import (
	"fmt"
	"math/rand"
)

// Cluster simulates a group of nodes exchanging Ping/Ack packets in
// discrete protocol periods. It exists to demonstrate and test gossip
// convergence without real sockets.
type Cluster struct {
	Nodes          []*Node
	Fanout         int // pings sent per node per period
	PiggybackLimit int // max events per packet header

	rng *rand.Rand
}

// NewCluster builds size fully interconnected nodes whose view tables are
// already in sync. seed makes the simulation deterministic.
func NewCluster(size, fanout, piggybackLimit int, seed int64) *Cluster {
	c := &Cluster{
		Fanout:         fanout,
		PiggybackLimit: piggybackLimit,
		rng:            rand.New(rand.NewSource(seed)),
	}
	for i := 0; i < size; i++ {
		c.Nodes = append(c.Nodes, c.newNode())
	}
	// Pre-existing members know each other without queueing gossip.
	for _, n := range c.Nodes {
		for _, other := range c.Nodes {
			if other.ID == n.ID {
				continue
			}
			n.view[other.ID] = &Member{ID: other.ID, Addr: other.Addr, State: Alive}
		}
	}
	return c
}

func (c *Cluster) newNode() *Node {
	id := fmt.Sprintf("node-%03d", len(c.Nodes))
	addr := fmt.Sprintf("127.0.0.1:%d", 7000+len(c.Nodes))
	dis := NewDisseminator(DefaultRetransmitMult, func() int { return len(c.Nodes) })
	return NewNode(id, addr, dis)
}

// Join adds a fresh node that contacts one seed member. The seed records the
// join event in its disseminator; the new node adopts the seed's view and
// also gossips its own arrival.
func (c *Cluster) Join(seedIdx int) *Node {
	n := c.newNode()
	seed := c.Nodes[seedIdx]
	for _, m := range seed.View() {
		n.Apply(Event{NodeID: m.ID, Addr: m.Addr, State: m.State, Incarnation: m.Incarnation})
	}
	join := Event{NodeID: n.ID, Addr: n.Addr, State: Alive, Incarnation: 0}
	seed.Apply(join)
	n.dis.Queue(join)
	c.Nodes = append(c.Nodes, n)
	return n
}

// RunPeriod simulates one protocol period: every node pings Fanout random
// peers, piggybacking change events on the Ping and on the Ack headers.
func (c *Cluster) RunPeriod() {
	for _, n := range c.Nodes {
		for i := 0; i < c.Fanout; i++ {
			peer := c.Nodes[c.rng.Intn(len(c.Nodes))]
			if peer == n {
				continue
			}
			ping := n.GossipEvents(c.PiggybackLimit)
			ack := peer.ReceivePing(ping, c.PiggybackLimit)
			n.ReceiveAck(ack)
		}
	}
}

// InformedCount reports how many nodes currently record id as Alive.
func (c *Cluster) InformedCount(id string) int {
	count := 0
	for _, n := range c.Nodes {
		if m, ok := n.Member(id); ok && m.State == Alive {
			count++
		}
	}
	return count
}
