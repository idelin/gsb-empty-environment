package main

import (
	"fmt"
	"sync"
	"time"
)

// Node is a storage node that serves queries independently of other nodes.
// It never shares locks with the gateway or with other nodes while a query
// is being processed, so queries on different nodes never block each other.
type Node struct {
	ID string

	mu        sync.Mutex
	processed int
	latency   time.Duration // simulated processing latency
	onQuery   func(key string)
}

// SetLatency sets the simulated per-query processing latency.
func (n *Node) SetLatency(d time.Duration) {
	n.mu.Lock()
	n.latency = d
	n.mu.Unlock()
}

// SetQueryHook installs a hook invoked (outside any lock) when a query
// arrives. Used by tests to simulate a blocked/slow node.
func (n *Node) SetQueryHook(hook func(key string)) {
	n.mu.Lock()
	n.onQuery = hook
	n.mu.Unlock()
}

// Processed returns the number of queries served so far.
func (n *Node) Processed() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.processed
}

// HandleQuery processes one query on this node.
func (n *Node) HandleQuery(key string) string {
	n.mu.Lock()
	hook := n.onQuery
	latency := n.latency
	n.mu.Unlock()

	if hook != nil {
		hook(key)
	}
	if latency > 0 {
		time.Sleep(latency)
	}

	n.mu.Lock()
	n.processed++
	n.mu.Unlock()
	return fmt.Sprintf("value(%s) served by %s", key, n.ID)
}
