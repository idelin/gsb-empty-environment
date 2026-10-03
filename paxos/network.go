package paxos

import "sync"

// Network is an in-process message transport with controllable partitions.
// Delivery is synchronous and per-pair FIFO, which keeps tests
// deterministic while still exercising the nodes' concurrency controls.
type Network struct {
	mu       sync.Mutex
	nodes    map[int]*Node
	blocked  map[[2]int]bool
	counters map[MsgType]int
	hook     func(Message) // observe-only, called outside the lock
}

// NewNetwork creates an empty transport.
func NewNetwork() *Network {
	return &Network{
		nodes:    make(map[int]*Node),
		blocked:  make(map[[2]int]bool),
		counters: make(map[MsgType]int),
	}
}

// Attach registers a node.
func (n *Network) Attach(node *Node) {
	n.mu.Lock()
	n.nodes[node.ID()] = node
	n.mu.Unlock()
}

// SetHook installs an observer invoked for every delivered message.
func (n *Network) SetHook(h func(Message)) {
	n.mu.Lock()
	n.hook = h
	n.mu.Unlock()
}

// Send delivers m unless the From->To link is partitioned.
func (n *Network) Send(m Message) {
	n.mu.Lock()
	if n.blocked[[2]int{m.From, m.To}] {
		n.mu.Unlock()
		return
	}
	n.counters[m.Type]++
	dst := n.nodes[m.To]
	hook := n.hook
	n.mu.Unlock()
	if hook != nil {
		hook(m)
	}
	if dst != nil {
		dst.Receive(m)
	}
}

// Count returns how many messages of type t have been delivered.
func (n *Network) Count(t MsgType) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.counters[t]
}

// BlockLink severs the directed from->to link only.
func (n *Network) BlockLink(from, to int) {
	n.mu.Lock()
	n.blocked[[2]int{from, to}] = true
	n.mu.Unlock()
}

// Block severs both directions of the a<->b link.
func (n *Network) Block(a, b int) {
	n.BlockLink(a, b)
	n.BlockLink(b, a)
}

// Partition severs all links between different groups (links within a
// group stay up).
func (n *Network) Partition(groups ...[]int) {
	for i := 0; i < len(groups); i++ {
		for j := i + 1; j < len(groups); j++ {
			for _, a := range groups[i] {
				for _, b := range groups[j] {
					n.Block(a, b)
				}
			}
		}
	}
}

// HealAll restores all links.
func (n *Network) HealAll() {
	n.mu.Lock()
	n.blocked = make(map[[2]int]bool)
	n.mu.Unlock()
}
