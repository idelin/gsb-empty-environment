package paxos

import (
	"sync"
	"sync/atomic"
	"time"
)

// Network is an in-memory transport with deterministic partition control and
// configurable simulated latency. It spawns no goroutines: delivery happens
// synchronously in the caller's goroutine, so shutdown is trivially leak-free.
type Network struct {
	mu        sync.RWMutex
	nodes     map[int]*Node
	cut       map[[2]int]bool // directional: cut[from,to] == dropped
	latency   time.Duration   // simulated one-way latency per delivered message
	dropped   atomic.Int64
	delivered atomic.Int64
}

// NewNetwork creates an empty transport.
func NewNetwork() *Network {
	return &Network{
		nodes: make(map[int]*Node),
		cut:   make(map[[2]int]bool),
	}
}

// SetLatency configures the simulated per-message delivery latency.
func (n *Network) SetLatency(d time.Duration) {
	n.mu.Lock()
	n.latency = d
	n.mu.Unlock()
}

func (n *Network) register(node *Node) {
	n.mu.Lock()
	n.nodes[node.id] = node
	n.mu.Unlock()
}

// Partition cuts all traffic between group A and group B (both directions).
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

// Isolate cuts a node off from every other node.
func (n *Network) Isolate(id int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for other := range n.nodes {
		if other != id {
			n.cut[[2]int{id, other}] = true
			n.cut[[2]int{other, id}] = true
		}
	}
}

// Heal removes all cuts.
func (n *Network) Heal() {
	n.mu.Lock()
	n.cut = make(map[[2]int]bool)
	n.mu.Unlock()
}

// Dropped / Delivered expose transport-level message statistics.
func (n *Network) Dropped() int64   { return n.dropped.Load() }
func (n *Network) Delivered() int64 { return n.delivered.Load() }

func (n *Network) route(from, to int) (*Node, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.cut[[2]int{from, to}] {
		n.dropped.Add(1)
		return nil, errf(ErrPartition, "link %d->%d is cut", from, to)
	}
	target, ok := n.nodes[to]
	if !ok {
		n.dropped.Add(1)
		return nil, errf(ErrPartition, "node %d unreachable", to)
	}
	n.delivered.Add(1)
	return target, nil
}

func (n *Network) sleep() {
	n.mu.RLock()
	d := n.latency
	n.mu.RUnlock()
	if d > 0 {
		time.Sleep(d)
	}
}

// Prepare sends a Phase-1a request from->to and returns the Promise.
func (n *Network) Prepare(from, to int, b Ballot) (Promise, error) {
	target, err := n.route(from, to)
	if err != nil {
		return Promise{}, err
	}
	n.sleep()
	return target.handlePrepare(b)
}

// Accept sends a Phase-2a request from->to. The returned Ballot is the
// acceptor's currently promised ballot (used for step-down detection).
func (n *Network) Accept(from, to int, b Ballot, e Entry) (Ballot, error) {
	target, err := n.route(from, to)
	if err != nil {
		return Ballot{}, err
	}
	n.sleep()
	return target.handleAccept(b, e)
}

// Commit broadcasts a committed entry from->to (best effort).
func (n *Network) Commit(from, to int, e Entry, commitIndex uint64) error {
	target, err := n.route(from, to)
	if err != nil {
		return err
	}
	n.sleep()
	return target.handleCommit(from, e, commitIndex)
}

// Fetch requests committed entries with Slot >= fromSlot from a peer.
func (n *Network) Fetch(from, to int, fromSlot uint64) ([]Entry, error) {
	target, err := n.route(from, to)
	if err != nil {
		return nil, err
	}
	n.sleep()
	return target.handleFetch(fromSlot), nil
}
