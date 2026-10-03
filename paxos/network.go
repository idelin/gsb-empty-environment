package paxos

import "sync"

// Network is an in-memory transport with programmable partitions.
// Delivery is a synchronous method call; a blocked link returns ErrUnreachable.
type Network struct {
	mu      sync.RWMutex
	nodes   map[int]*Node
	blocked map[[2]int]bool
}

func NewNetwork() *Network {
	return &Network{nodes: make(map[int]*Node), blocked: make(map[[2]int]bool)}
}

func (n *Network) register(node *Node) {
	n.mu.Lock()
	n.nodes[node.id] = node
	n.mu.Unlock()
}

// Cut blocks traffic between every node of group a and every node of group b.
func (n *Network) Cut(a, b []int) {
	n.mu.Lock()
	for _, x := range a {
		for _, y := range b {
			if x == y {
				continue
			}
			n.blocked[[2]int{x, y}] = true
			n.blocked[[2]int{y, x}] = true
		}
	}
	n.mu.Unlock()
}

// Isolate cuts one node off from all the others.
func (n *Network) Isolate(id int) {
	n.mu.Lock()
	for other := range n.nodes {
		if other == id {
			continue
		}
		n.blocked[[2]int{id, other}] = true
		n.blocked[[2]int{other, id}] = true
	}
	n.mu.Unlock()
}

// Heal removes all partitions.
func (n *Network) Heal() {
	n.mu.Lock()
	n.blocked = make(map[[2]int]bool)
	n.mu.Unlock()
}

func (n *Network) reachable(from, to int) bool {
	if from == to {
		return true
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return !n.blocked[[2]int{from, to}]
}

func (n *Network) peerIDs(self int) []int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	ids := make([]int, 0, len(n.nodes))
	for id := range n.nodes {
		if id != self {
			ids = append(ids, id)
		}
	}
	return ids
}

func (n *Network) get(id int) *Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nodes[id]
}

func (n *Network) size() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.nodes)
}
