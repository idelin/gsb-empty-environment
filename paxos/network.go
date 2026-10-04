package paxos

import (
	"sort"
	"sync"
)

// Network is a synchronous in-memory transport. RPCs are direct function
// calls, which makes tests fully deterministic. Links between any pair of
// nodes can be blocked to simulate network partitions.
type Network struct {
	mu       sync.Mutex
	nodes    map[int]*Node
	blocked  map[[2]int]bool
	prepares int // Phase 1a messages delivered (test instrumentation)
	accepts  int // Phase 2a messages delivered
	commits  int // commit notices delivered
}

func NewNetwork() *Network {
	return &Network{
		nodes:   make(map[int]*Node),
		blocked: make(map[[2]int]bool),
	}
}

func (n *Network) register(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.id] = node
}

// Size returns the number of registered nodes.
func (n *Network) Size() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.nodes)
}

// Majority returns the quorum size.
func (n *Network) Majority() int { return n.Size()/2 + 1 }

// Block cuts communication between a and b (both directions).
func (n *Network) Block(a, b int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked[[2]int{a, b}] = true
	n.blocked[[2]int{b, a}] = true
}

// Unblock restores communication between a and b.
func (n *Network) Unblock(a, b int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.blocked, [2]int{a, b})
	delete(n.blocked, [2]int{b, a})
}

// Partition cuts all links between groupA and groupB.
func (n *Network) Partition(groupA, groupB []int) {
	for _, a := range groupA {
		for _, b := range groupB {
			n.Block(a, b)
		}
	}
}

// HealAll restores all links.
func (n *Network) HealAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = make(map[[2]int]bool)
}

func (n *Network) reachable(from, to int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return !n.blocked[[2]int{from, to}]
}

func (n *Network) get(id int) *Node {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.nodes[id]
}

// peers returns the sorted ids of all other nodes.
func (n *Network) peers(id int) []int {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]int, 0, len(n.nodes)-1)
	for pid := range n.nodes {
		if pid != id {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out
}

// Stats returns delivered message counts (prepares, accepts, commits).
func (n *Network) Stats() (prepares, accepts, commits int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.prepares, n.accepts, n.commits
}

func (n *Network) sendPrepare(from, to int, req PrepareRequest) (Promise, error) {
	if !n.reachable(from, to) {
		return Promise{}, ErrUnreachable
	}
	node := n.get(to)
	if node == nil {
		return Promise{}, ErrUnreachable
	}
	n.mu.Lock()
	n.prepares++
	n.mu.Unlock()
	return node.handlePrepare(req), nil
}

func (n *Network) sendAccept(from, to int, req AcceptRequest) (Accepted, error) {
	if !n.reachable(from, to) {
		return Accepted{}, ErrUnreachable
	}
	node := n.get(to)
	if node == nil {
		return Accepted{}, ErrUnreachable
	}
	n.mu.Lock()
	n.accepts++
	n.mu.Unlock()
	return node.handleAccept(req), nil
}

func (n *Network) sendCommit(from, to int, c CommitNotice) error {
	if !n.reachable(from, to) {
		return ErrUnreachable
	}
	node := n.get(to)
	if node == nil {
		return ErrUnreachable
	}
	n.mu.Lock()
	n.commits++
	n.mu.Unlock()
	node.handleCommit(c)
	return nil
}

func (n *Network) sendSync(from, to int, fromSlot uint64) ([]Entry, error) {
	if !n.reachable(from, to) {
		return nil, ErrUnreachable
	}
	node := n.get(to)
	if node == nil {
		return nil, ErrUnreachable
	}
	return node.handleSync(fromSlot), nil
}
