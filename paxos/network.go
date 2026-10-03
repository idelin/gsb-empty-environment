package paxos

import "sync"

// Network is a synchronous in-memory Transport with partition support and
// message counters (used to verify that a leader with a valid lease never
// re-runs Phase 1).
type Network struct {
	mu       sync.Mutex
	nodes    map[int]*Node
	blocked  map[[2]int]bool
	prepares int64
	accepts  int64
	commits  int64
}

// NewNetwork creates an empty network.
func NewNetwork() *Network {
	return &Network{
		nodes:   make(map[int]*Node),
		blocked: make(map[[2]int]bool),
	}
}

// Attach registers a node with the network.
func (n *Network) Attach(node *Node) {
	n.mu.Lock()
	n.nodes[node.id] = node
	n.mu.Unlock()
	node.attach(n)
}

// Partition splits the cluster into the given groups; traffic between
// different groups is dropped, traffic within a group flows.
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
			if a != b && groupOf[a] != groupOf[b] {
				n.blocked[[2]int{a, b}] = true
			}
		}
	}
}

// Heal removes all partitions.
func (n *Network) Heal() {
	n.mu.Lock()
	n.blocked = make(map[[2]int]bool)
	n.mu.Unlock()
}

func (n *Network) reachable(from, to int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return !n.blocked[[2]int{from, to}]
}

// Stats returns the number of Prepare/Accept/Commit messages delivered.
func (n *Network) Stats() (prepares, accepts, commits int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.prepares, n.accepts, n.commits
}

// Prepare routes a Phase-1a message. The second return value reports
// reachability (false simulates a dropped packet).
func (n *Network) Prepare(from, to int, req PrepareReq) (PrepareResp, bool) {
	if !n.reachable(from, to) {
		return PrepareResp{}, false
	}
	n.mu.Lock()
	peer := n.nodes[to]
	n.prepares++
	n.mu.Unlock()
	return peer.handlePrepare(req), true
}

// Accept routes a Phase-2a message.
func (n *Network) Accept(from, to int, req AcceptReq) (AcceptResp, bool) {
	if !n.reachable(from, to) {
		return AcceptResp{}, false
	}
	n.mu.Lock()
	peer := n.nodes[to]
	n.accepts++
	n.mu.Unlock()
	return peer.handleAccept(req), true
}

// Commit routes a commit notification.
func (n *Network) Commit(from, to int, req CommitReq) bool {
	if !n.reachable(from, to) {
		return false
	}
	n.mu.Lock()
	peer := n.nodes[to]
	n.commits++
	n.mu.Unlock()
	return peer.handleCommit(req)
}
