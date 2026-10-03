package paxos

import (
	"errors"
	"sync"
)

// ErrUnreachable is returned when a link is down (partition) or unknown.
var ErrUnreachable = errors.New("paxos: peer unreachable")

// Network is a synchronous in-memory transport. Delivery happens on the
// caller's goroutine, so the core spawns no background goroutines at all.
// It supports arbitrary link failures to simulate network partitions.
type Network struct {
	mu      sync.RWMutex
	nodes   map[string]*Node
	blocked map[[2]string]bool
}

func NewNetwork() *Network {
	return &Network{nodes: make(map[string]*Node), blocked: make(map[[2]string]bool)}
}

func (n *Network) Register(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.id] = node
}

// Send delivers req to `to` synchronously and returns its response.
func (n *Network) Send(from, to string, req Request) (Response, error) {
	n.mu.RLock()
	if n.blocked[[2]string{from, to}] {
		n.mu.RUnlock()
		return nil, ErrUnreachable
	}
	peer := n.nodes[to]
	n.mu.RUnlock()
	if peer == nil {
		return nil, ErrUnreachable
	}
	return peer.Handle(req), nil
}

// SetLink enables/disables both directions of the a<->b link.
func (n *Network) SetLink(a, b string, up bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if up {
		delete(n.blocked, [2]string{a, b})
		delete(n.blocked, [2]string{b, a})
		return
	}
	n.blocked[[2]string{a, b}] = true
	n.blocked[[2]string{b, a}] = true
}

// Partition cuts all links between groupA and groupB.
func (n *Network) Partition(groupA, groupB []string) {
	for _, a := range groupA {
		for _, b := range groupB {
			n.SetLink(a, b, false)
		}
	}
}

// Isolate cuts id off from every other registered node.
func (n *Network) Isolate(id string) {
	n.mu.RLock()
	others := make([]string, 0, len(n.nodes))
	for o := range n.nodes {
		if o != id {
			others = append(others, o)
		}
	}
	n.mu.RUnlock()
	n.Partition([]string{id}, others)
}

// Heal restores all links.
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = make(map[[2]string]bool)
}
