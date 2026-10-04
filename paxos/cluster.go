package paxos

import (
	"sync"
	"time"
)

// Cluster is an in-memory Transport simulating a network of nodes. It
// supports partitions and message counting for tests and demos.
type Cluster struct {
	mu      sync.Mutex
	nodes   []*Node
	stores  []*Store
	blocked map[[2]int]bool
	counts  map[MsgType]int
}

// NewCluster builds n nodes (ids 0..n-1). If storePaths is non-nil it must
// have length n and each node persists to the given file.
func NewCluster(n int, leaseDur time.Duration, storePaths []string) (*Cluster, error) {
	c := &Cluster{
		blocked: make(map[[2]int]bool),
		counts:  make(map[MsgType]int),
	}
	peers := make([]int, n)
	for i := range peers {
		peers[i] = i
	}
	for i := 0; i < n; i++ {
		var st *Store
		if storePaths != nil {
			var err error
			st, err = OpenStore(storePaths[i], false)
			if err != nil {
				return nil, err
			}
		}
		node, err := NewNode(i, peers, st, leaseDur, c)
		if err != nil {
			return nil, err
		}
		c.nodes = append(c.nodes, node)
		c.stores = append(c.stores, st)
		node.Start()
	}
	return c, nil
}

// Send implements Transport.
func (c *Cluster) Send(to int, m Msg) {
	c.mu.Lock()
	if c.blocked[[2]int{m.From, to}] {
		c.mu.Unlock()
		return
	}
	c.counts[m.Type]++
	node := c.nodes[to]
	c.mu.Unlock()
	node.Deliver(m)
}

// Node returns the i-th node.
func (c *Cluster) Node(i int) *Node { return c.nodes[i] }

// Size returns the number of nodes.
func (c *Cluster) Size() int { return len(c.nodes) }

// Partition splits the cluster into isolated groups: links between nodes in
// different groups are dropped in both directions; links within a group stay
// up. Passing a single group isolates those nodes from all others.
func (c *Cluster) Partition(groups ...[]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	groupOf := make(map[int]int)
	for gi, g := range groups {
		for _, id := range g {
			groupOf[id] = gi
		}
	}
	for i := range c.nodes {
		for j := range c.nodes {
			if i == j {
				continue
			}
			gi, iok := groupOf[i]
			gj, jok := groupOf[j]
			if !iok || !jok || gi != gj {
				c.blocked[[2]int{i, j}] = true
			}
		}
	}
}

// Heal removes all partitions.
func (c *Cluster) Heal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = make(map[[2]int]bool)
}

// Block drops all messages from one node to another (directed).
func (c *Cluster) Block(from, to int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked[[2]int{from, to}] = true
}

// Unblock re-enables a directed link previously cut with Block.
func (c *Cluster) Unblock(from, to int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.blocked, [2]int{from, to})
}

// Counts returns a snapshot of per-message-type counters.
func (c *Cluster) Counts() map[MsgType]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[MsgType]int, len(c.counts))
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}

// ResetCounts zeroes the message counters.
func (c *Cluster) ResetCounts() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts = make(map[MsgType]int)
}

// Close stops all nodes and closes their stores.
func (c *Cluster) Close() {
	for _, n := range c.nodes {
		n.Close()
	}
	for _, s := range c.stores {
		if s != nil {
			s.Close()
		}
	}
}
