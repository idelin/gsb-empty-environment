package paxos

import "sync"

// Network is an in-memory transport with programmable partitions. Delivery is
// synchronous and deterministic, which keeps tests reproducible; links cut by
// Partition silently drop traffic exactly like a real net-split.
type Network struct {
	mu      sync.RWMutex
	nodes   map[int]*Node
	blocked map[[2]int]bool
}

func NewNetwork() *Network {
	return &Network{nodes: make(map[int]*Node), blocked: make(map[[2]int]bool)}
}

func (n *Network) Add(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.id] = node
}

func (n *Network) Size() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.nodes)
}

// Partition blocks all traffic between different groups. Traffic inside a
// group flows freely. Any node not listed is isolated from everyone.
func (n *Network) Partition(groups ...[]int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	groupOf := make(map[int]int)
	for gi, g := range groups {
		for _, id := range g {
			groupOf[id] = gi
		}
	}
	for a := range n.nodes {
		for b := range n.nodes {
			if a == b {
				continue
			}
			ga, oka := groupOf[a]
			gb, okb := groupOf[b]
			if !oka || !okb || ga != gb {
				n.blocked[[2]int{a, b}] = true
			}
		}
	}
}

// Heal removes all partitions.
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = make(map[[2]int]bool)
}

// route resolves the destination node if the directed link from->to is up.
func (n *Network) route(from, to int) (*Node, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.blocked[[2]int{from, to}] {
		return nil, false
	}
	node, ok := n.nodes[to]
	return node, ok
}

func (n *Network) peers(of int) []int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]int, 0, len(n.nodes)-1)
	for id := range n.nodes {
		if id != of {
			out = append(out, id)
		}
	}
	return out
}
