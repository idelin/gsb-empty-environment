package paxos

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Network is an in-memory, synchronous transport used to model a
// cluster. Delivery is a direct method call on the target node, which
// keeps the whole system free of background goroutines. Partitions are
// simulated with directed cuts.
type Network struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	cuts  map[[2]string]bool

	prepares int64
	accepts  int64
	commits  int64
}

func NewNetwork() *Network {
	return &Network{
		nodes: make(map[string]*Node),
		cuts:  make(map[[2]string]bool),
	}
}

func (n *Network) Register(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.id] = node
}

// Members returns the sorted ids of all registered nodes.
func (n *Network) Members() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]string, 0, len(n.nodes))
	for id := range n.nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Send delivers msg synchronously. It reports false when the link is cut
// or the destination does not exist. The network lock is never held while
// the receiver handler runs, so delivery cannot deadlock against
// partition reconfiguration.
func (n *Network) Send(from, to string, msg Message) (Message, bool) {
	switch msg.(type) {
	case Prepare:
		atomic.AddInt64(&n.prepares, 1)
	case Accept:
		atomic.AddInt64(&n.accepts, 1)
	case Commit:
		atomic.AddInt64(&n.commits, 1)
	}
	n.mu.RLock()
	cut := n.cuts[[2]string{from, to}]
	target := n.nodes[to]
	n.mu.RUnlock()
	if cut || target == nil {
		return nil, false
	}
	return target.Handle(msg), true
}

// Cut severs the link between a and b in both directions.
func (n *Network) Cut(a, b string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cuts[[2]string{a, b}] = true
	n.cuts[[2]string{b, a}] = true
}

// Isolate disconnects id from every other node (both directions).
// A node always remains reachable from itself.
func (n *Network) Isolate(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for other := range n.nodes {
		if other == id {
			continue
		}
		n.cuts[[2]string{id, other}] = true
		n.cuts[[2]string{other, id}] = true
	}
}

// Heal removes all cuts.
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cuts = make(map[[2]string]bool)
}

// Stats returns the total number of Prepare/Accept/Commit messages sent,
// used by tests to assert that the leader lease really skips Phase 1.
func (n *Network) Stats() (prepares, accepts, commits int64) {
	return atomic.LoadInt64(&n.prepares),
		atomic.LoadInt64(&n.accepts),
		atomic.LoadInt64(&n.commits)
}
