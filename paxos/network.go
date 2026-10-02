package paxos

import (
	"sort"
	"sync"
)

// Network is a deterministic, synchronous in-memory transport used to
// model a cluster. It supports arbitrary partitions and healing. Message
// delivery is a direct method call, so no background goroutines exist
// and tests are fully deterministic.
type Network struct {
	mu      sync.RWMutex
	nodes   map[int]*Node
	blocked map[[2]int]bool
}

// NewNetwork creates an empty simulated network.
func NewNetwork() *Network {
	return &Network{
		nodes:   make(map[int]*Node),
		blocked: make(map[[2]int]bool),
	}
}

// Register adds a node to the network.
func (n *Network) Register(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.id] = node
}

// Size returns the number of registered nodes.
func (n *Network) Size() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.nodes)
}

// Majority returns the quorum size for the cluster.
func (n *Network) Majority() int {
	return n.Size()/2 + 1
}

// Peers returns the sorted IDs of all nodes except id.
func (n *Network) Peers(id int) []int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]int, 0, len(n.nodes)-1)
	for pid := range n.nodes {
		if pid != id {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out
}

// Partition cuts all links between the given groups of node IDs. Links
// inside a group stay up. Any previous partition is replaced.
func (n *Network) Partition(groups ...[]int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = make(map[[2]int]bool)
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
			if oka && okb && ga != gb {
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

// Connected reports whether a can currently send to b.
func (n *Network) Connected(a, b int) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return !n.blocked[[2]int{a, b}]
}

// Prepare delivers a Phase 1a message; ok is false if the link is cut.
func (n *Network) Prepare(from, to int, req PrepareRequest) (resp PrepareResponse, ok bool) {
	n.mu.RLock()
	target, up := n.nodes[to], !n.blocked[[2]int{from, to}]
	n.mu.RUnlock()
	if !up || target == nil {
		return PrepareResponse{}, false
	}
	return target.HandlePrepare(req), true
}

// Accept delivers a Phase 2a message; ok is false if the link is cut.
func (n *Network) Accept(from, to int, req AcceptRequest) (resp AcceptResponse, ok bool) {
	n.mu.RLock()
	target, up := n.nodes[to], !n.blocked[[2]int{from, to}]
	n.mu.RUnlock()
	if !up || target == nil {
		return AcceptResponse{}, false
	}
	return target.HandleAccept(req), true
}

// Sync delivers an anti-entropy message; ok is false if the link is cut.
func (n *Network) Sync(from, to int, req SyncRequest) (resp SyncResponse, ok bool) {
	n.mu.RLock()
	target, up := n.nodes[to], !n.blocked[[2]int{from, to}]
	n.mu.RUnlock()
	if !up || target == nil {
		return SyncResponse{}, false
	}
	return target.HandleSync(req), true
}
