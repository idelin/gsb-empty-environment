package paxos

import (
	"sync"
	"sync/atomic"
)

// Cluster is an in-memory Transport with controllable network partitions.
// Delivery is synchronous, which keeps tests deterministic. Safe for
// concurrent use.
type Cluster struct {
	nodes     []*Node
	mu        sync.RWMutex
	blocked   map[[2]int]bool
	delivered atomic.Int64
	dropped   atomic.Int64
}

// NewCluster creates n nodes and a fully connected transport.
func NewCluster(n int) *Cluster {
	c := &Cluster{blocked: make(map[[2]int]bool)}
	for i := 0; i < n; i++ {
		c.nodes = append(c.nodes, NewNode(i))
	}
	return c
}

// Node returns the i-th node.
func (c *Cluster) Node(i int) *Node { return c.nodes[i] }

// Size returns the cluster size.
func (c *Cluster) Size() int { return len(c.nodes) }

// Send implements Transport.
func (c *Cluster) Send(from, to int, msg any) (any, error) {
	c.mu.RLock()
	blocked := c.blocked[[2]int{from, to}]
	c.mu.RUnlock()
	if blocked {
		c.dropped.Add(1)
		return nil, ErrUnreachable
	}
	c.delivered.Add(1)
	return c.nodes[to].Handle(msg), nil
}

// Partition cuts all traffic between groupA and groupB (both directions).
func (c *Cluster) Partition(groupA, groupB []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range groupA {
		for _, b := range groupB {
			c.blocked[[2]int{a, b}] = true
			c.blocked[[2]int{b, a}] = true
		}
	}
}

// Isolate cuts node id off from every other node.
func (c *Cluster) Isolate(id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.nodes {
		if i == id {
			continue
		}
		c.blocked[[2]int{id, i}] = true
		c.blocked[[2]int{i, id}] = true
	}
}

// Heal removes all partitions.
func (c *Cluster) Heal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = make(map[[2]int]bool)
}

// Stats returns delivered/dropped message counts.
func (c *Cluster) Stats() (delivered, dropped int64) {
	return c.delivered.Load(), c.dropped.Load()
}

// Peers returns the ids of all nodes except self.
func (c *Cluster) Peers(self int) []int {
	out := make([]int, 0, len(c.nodes)-1)
	for i := range c.nodes {
		if i != self {
			out = append(out, i)
		}
	}
	return out
}
