package paxos

import (
	"sync"
	"time"
)

// Network is an in-memory message transport with partition simulation and
// per-type message statistics (used to prove Phase 1 is skipped under a
// valid leader lease).
type Network struct {
	mu     sync.Mutex
	nodes  map[int]*Node
	cut    map[[2]int]bool
	stats  map[MsgType]int
	closed bool
}

// NewNetwork creates a transport for a cluster of the given size. leaseDur
// is the leader lease duration granted on election.
func NewNetwork(size int, leaseDur time.Duration) *Network {
	n := &Network{
		nodes: make(map[int]*Node),
		cut:   make(map[[2]int]bool),
		stats: make(map[MsgType]int),
	}
	for i := 0; i < size; i++ {
		n.nodes[i] = newNode(i, size, n, leaseDur)
	}
	return n
}

// Node returns the node with the given id.
func (n *Network) Node(id int) *Node { return n.nodes[id] }

// Send delivers a message unless the link is cut or the network is closed.
func (n *Network) Send(m Message) {
	n.mu.Lock()
	if n.closed || n.cut[[2]int{m.From, m.To}] {
		n.mu.Unlock()
		return
	}
	n.stats[m.Type]++
	node := n.nodes[m.To]
	n.mu.Unlock()
	if node != nil {
		node.enqueue(m)
	}
}

// Partition severs all links (both directions) between groups a and b.
func (n *Network) Partition(a, b []int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, x := range a {
		for _, y := range b {
			n.cut[[2]int{x, y}] = true
			n.cut[[2]int{y, x}] = true
		}
	}
}

// Heal restores all links.
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cut = make(map[[2]int]bool)
}

// Stats returns a snapshot of per-type delivered message counts.
func (n *Network) Stats() map[MsgType]int {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[MsgType]int, len(n.stats))
	for k, v := range n.stats {
		out[k] = v
	}
	return out
}

// ResetStats clears the message statistics.
func (n *Network) ResetStats() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stats = make(map[MsgType]int)
}

// Close stops every node; after Close returns no goroutines remain.
func (n *Network) Close() {
	n.mu.Lock()
	n.closed = true
	nodes := make([]*Node, 0, len(n.nodes))
	for _, nd := range n.nodes {
		nodes = append(nodes, nd)
	}
	n.mu.Unlock()
	for _, nd := range nodes {
		nd.Stop()
	}
}
