package leaserouter

import (
	"fmt"
	"sync"
	"time"
)

// Node is a storage node. Each node serializes its own requests with a
// private mutex, so a slow or blocked node never stalls requests that
// are served by a different node.
type Node struct {
	ID      string
	Latency time.Duration // simulated per-request processing time

	mu    sync.Mutex
	store map[string]string
}

// NewNode creates a node with the given simulated processing latency.
func NewNode(id string, latency time.Duration) *Node {
	return &Node{ID: id, Latency: latency, store: make(map[string]string)}
}

// Put stores a key/value pair on the node.
func (n *Node) Put(key, value string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.store[key] = value
}

// Get reads a key from the node, holding only this node's lock.
func (n *Node) Get(key string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Latency > 0 {
		time.Sleep(n.Latency)
	}
	v, ok := n.store[key]
	if !ok {
		return "", fmt.Errorf("node %s: key %q not found", n.ID, key)
	}
	return v, nil
}
