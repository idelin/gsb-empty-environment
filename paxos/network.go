package paxos

import "sync"

// Network is an in-memory, fully deterministic transport. Delivery is
// synchronous and FIFO-per-sender; directed edges can be blocked to simulate
// network partitions. The network lock is never held while a node lock is
// acquired, which keeps the lock order cycle-free.
type Network struct {
	mu      sync.Mutex
	nodes   map[int]*Node
	blocked map[[2]int]bool
	counts  map[MsgKind]int
}

// NewNetwork creates an empty transport.
func NewNetwork() *Network {
	return &Network{
		nodes:   make(map[int]*Node),
		blocked: make(map[[2]int]bool),
		counts:  make(map[MsgKind]int),
	}
}

// NewCluster builds a cluster of size nodes with deterministic per-node
// election timeouts (smaller ids campaign first).
func NewCluster(size int) (*Network, []*Node) {
	net := NewNetwork()
	nodes := make([]*Node, size)
	for i := 0; i < size; i++ {
		nodes[i] = newNode(Config{
			ID:                i,
			ClusterSize:       size,
			ElectionTimeout:   10 + i*3,
			HeartbeatInterval: 4,
			LeaseDuration:     12,
		}, net)
		net.nodes[i] = nodes[i]
	}
	return net, nodes
}

// Send delivers one message synchronously unless the edge is blocked.
func (net *Network) Send(m Message) {
	net.mu.Lock()
	if net.blocked[[2]int{m.From, m.To}] {
		net.mu.Unlock()
		return
	}
	target := net.nodes[m.To]
	net.counts[m.Kind]++
	net.mu.Unlock()
	if target != nil {
		target.HandleMessage(m)
	}
}

func (net *Network) sendAll(out []Message) {
	for _, m := range out {
		net.Send(m)
	}
}

// SetPartition blocks all traffic between different groups. Traffic inside
// a group flows. Calling SetPartition() or Heal restores full connectivity.
func (net *Network) SetPartition(groups ...[]int) {
	net.mu.Lock()
	defer net.mu.Unlock()
	net.blocked = make(map[[2]int]bool)
	groupOf := make(map[int]int)
	for g, members := range groups {
		for _, id := range members {
			groupOf[id] = g
		}
	}
	for a := range net.nodes {
		for b := range net.nodes {
			if a != b && groupOf[a] != groupOf[b] {
				net.blocked[[2]int{a, b}] = true
			}
		}
	}
}

// Heal removes all blocked edges.
func (net *Network) Heal() { net.SetPartition() }

// Count returns how many messages of kind have been delivered since the
// last ResetCounts.
func (net *Network) Count(kind MsgKind) int {
	net.mu.Lock()
	defer net.mu.Unlock()
	return net.counts[kind]
}

// ResetCounts clears the delivered-message statistics.
func (net *Network) ResetCounts() {
	net.mu.Lock()
	defer net.mu.Unlock()
	net.counts = make(map[MsgKind]int)
}
