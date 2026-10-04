package merge

import (
	"sort"
	"sync"
)

// Network is a simulated switch. It can be partitioned so that messages
// between two groups are silently dropped, and healed again. Delivery is
// asynchronous (buffered per-node inboxes), like a real datagram network.
type Network struct {
	mu      sync.RWMutex
	nodes   map[int]*Node
	blocked map[[2]int]bool
}

func NewNetwork() *Network {
	return &Network{
		nodes:   make(map[int]*Node),
		blocked: make(map[[2]int]bool),
	}
}

func (nt *Network) Add(n *Node) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	nt.nodes[n.id] = n
}

// Nodes returns all registered nodes sorted by ID.
func (nt *Network) Nodes() []*Node {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	out := make([]*Node, 0, len(nt.nodes))
	for _, n := range nt.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func pair(a, b int) [2]int {
	if a > b {
		a, b = b, a
	}
	return [2]int{a, b}
}

// Partition drops all traffic between groupA and groupB (both directions).
func (nt *Network) Partition(groupA, groupB []int) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	for _, a := range groupA {
		for _, b := range groupB {
			nt.blocked[pair(a, b)] = true
		}
	}
}

// Heal removes all partition blocks.
func (nt *Network) Heal() {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	nt.blocked = make(map[[2]int]bool)
}

// Send attempts delivery of one message. It returns false if the destination
// is unknown, the link is partitioned, or the inbox is full (message lost).
func (nt *Network) Send(m Message) bool {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	dst, ok := nt.nodes[m.To]
	if !ok {
		return false
	}
	if nt.blocked[pair(m.From, m.To)] {
		return false
	}
	select {
	case dst.inbox <- m:
		return true
	default:
		return false
	}
}

func (nt *Network) SendAll(ms []Message) {
	for _, m := range ms {
		nt.Send(m)
	}
}

// PumpAll synchronously drains every node's inbox until the system is
// quiescent. It gives tests a deterministic, goroutine-free way to run the
// protocol to completion; if the protocol ever livelocked (messages bouncing
// forever) this function would not return, so tests also guard against that.
func PumpAll(net *Network) {
	for {
		progress := false
		for _, n := range net.Nodes() {
			if n.drain() {
				progress = true
			}
		}
		if !progress {
			return
		}
	}
}

// Converged reports whether every node agrees on cluster ID and epoch and
// knows every other node.
func Converged(net *Network) bool {
	nodes := net.Nodes()
	if len(nodes) == 0 {
		return true
	}
	cid := nodes[0].ClusterID()
	epoch := nodes[0].Epoch()
	for _, n := range nodes {
		if n.ClusterID() != cid || n.Epoch() != epoch {
			return false
		}
		if len(n.Members()) != len(nodes) {
			return false
		}
	}
	return true
}
