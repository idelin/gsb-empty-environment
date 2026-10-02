package swim

import "sync"

// Transport delivers messages between nodes.
type Transport interface {
	Send(from, to string, msg Message)
}

// Network is an in-memory Transport with controllable per-link
// packet loss. It is used by the demo and the tests to simulate
// weak-network conditions.
type Network struct {
	mu      sync.Mutex
	inboxes map[string]chan<- Message
	drops   map[[2]string]bool
}

func NewNetwork() *Network {
	return &Network{
		inboxes: make(map[string]chan<- Message),
		drops:   make(map[[2]string]bool),
	}
}

// Attach registers the inbox of a node.
func (n *Network) Attach(id string, inbox chan<- Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.inboxes[id] = inbox
}

// Drop makes the directed link from->to silently drop all packets.
func (n *Network) Drop(from, to string) { n.setLink(from, to, true) }

// Heal restores a previously dropped directed link.
func (n *Network) Heal(from, to string) { n.setLink(from, to, false) }

func (n *Network) setLink(from, to string, drop bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.drops[[2]string{from, to}] = drop
}

func (n *Network) Send(from, to string, msg Message) {
	n.mu.Lock()
	drop := n.drops[[2]string{from, to}]
	inbox := n.inboxes[to]
	n.mu.Unlock()
	if drop || inbox == nil {
		return
	}
	select {
	case inbox <- msg:
	default: // inbox full: treat as packet loss
	}
}
