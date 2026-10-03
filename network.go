package paxos

import (
	"sync"
	"time"
)

// Network is an in-memory transport with programmable partitions. It
// delivers RPCs synchronously to the callee's handlers; broadcasts fan out
// to peers in parallel goroutines that are always joined before returning,
// so no goroutine ever outlives the call that spawned it.
type Network struct {
	mu    sync.RWMutex
	nodes []*Node
	cut   map[[2]int]bool
}

// NewNetwork creates a fully connected cluster of n nodes.
func NewNetwork(n int, lease time.Duration) *Network {
	net := &Network{cut: make(map[[2]int]bool)}
	net.nodes = make([]*Node, n)
	for i := 0; i < n; i++ {
		net.nodes[i] = newNode(i, n, net, lease)
	}
	return net
}

// Node returns the i-th node (0-based).
func (n *Network) Node(i int) *Node { return n.nodes[i] }

// Size returns the cluster size.
func (n *Network) Size() int { return len(n.nodes) }

// Cut severs traffic between a and b in both directions.
func (n *Network) Cut(a, b int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cut[[2]int{a, b}] = true
	n.cut[[2]int{b, a}] = true
}

// Heal restores traffic between a and b.
func (n *Network) Heal(a, b int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.cut, [2]int{a, b})
	delete(n.cut, [2]int{b, a})
}

// Partition cuts all links between the given groups, leaving links inside
// each group intact.
func (n *Network) Partition(groups ...[]int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	groupOf := make(map[int]int)
	for gi, g := range groups {
		for _, id := range g {
			groupOf[id] = gi
		}
	}
	for i := range n.nodes {
		for j := i + 1; j < len(n.nodes); j++ {
			gi, iok := groupOf[i]
			gj, jok := groupOf[j]
			if iok && jok && gi != gj {
				n.cut[[2]int{i, j}] = true
				n.cut[[2]int{j, i}] = true
			}
		}
	}
}

// HealAll removes all partitions.
func (n *Network) HealAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cut = make(map[[2]int]bool)
}

// peers returns the nodes reachable from `from` (excluding itself).
func (n *Network) peers(from int) []*Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	var out []*Node
	for i, nd := range n.nodes {
		if i == from || n.cut[[2]int{from, i}] {
			continue
		}
		out = append(out, nd)
	}
	return out
}

func (n *Network) broadcastPrepare(from int, req PrepareRequest) []PrepareResponse {
	peers := n.peers(from)
	resps := make([]PrepareResponse, len(peers))
	var wg sync.WaitGroup
	for i, nd := range peers {
		wg.Add(1)
		go func(i int, nd *Node) {
			defer wg.Done()
			resps[i] = nd.handlePrepare(req)
		}(i, nd)
	}
	wg.Wait()
	return resps
}

func (n *Network) broadcastAccept(from int, req AcceptRequest) []AcceptResponse {
	peers := n.peers(from)
	resps := make([]AcceptResponse, len(peers))
	var wg sync.WaitGroup
	for i, nd := range peers {
		wg.Add(1)
		go func(i int, nd *Node) {
			defer wg.Done()
			resps[i] = nd.handleAccept(req)
		}(i, nd)
	}
	wg.Wait()
	return resps
}

func (n *Network) broadcastCommit(from int, req CommitRequest) {
	peers := n.peers(from)
	var wg sync.WaitGroup
	for _, nd := range peers {
		wg.Add(1)
		go func(nd *Node) {
			defer wg.Done()
			nd.handleCommit(req)
		}(nd)
	}
	wg.Wait()
}
