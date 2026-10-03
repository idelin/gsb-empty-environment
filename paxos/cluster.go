package paxos

import "sync"

// Cluster is a synchronous in-memory transport with partition control.
// It spawns no background goroutines: every message is a direct method
// call, which keeps the engine free of goroutine leaks by construction.
type Cluster struct {
	mu      sync.Mutex
	nodes   []*Node
	blocked map[[2]int]bool
}

// NewCluster builds a fully connected cluster of n nodes (ids 0..n-1).
func NewCluster(n int) *Cluster {
	c := &Cluster{blocked: make(map[[2]int]bool)}
	for i := 0; i < n; i++ {
		c.nodes = append(c.nodes, newNode(i, c))
	}
	return c
}

// Node returns the node with the given id.
func (c *Cluster) Node(id int) *Node { return c.nodes[id] }

// Nodes returns all nodes in id order.
func (c *Cluster) Nodes() []*Node { return c.nodes }

// Size returns the number of nodes.
func (c *Cluster) Size() int { return len(c.nodes) }

// Quorum returns the majority quorum size.
func (c *Cluster) Quorum() int { return len(c.nodes)/2 + 1 }

// SetLink connects or disconnects the bidirectional link a<->b.
func (c *Cluster) SetLink(a, b int, up bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range [][2]int{{a, b}, {b, a}} {
		if up {
			delete(c.blocked, p)
		} else {
			c.blocked[p] = true
		}
	}
}

// Partition blocks all links between different groups. Nodes not listed
// in any group are isolated from every group.
func (c *Cluster) Partition(groups ...[]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	groupOf := make(map[int]int, len(c.nodes))
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
			if groupOf[i] != groupOf[j] {
				c.blocked[[2]int{i, j}] = true
			} else {
				delete(c.blocked, [2]int{i, j})
			}
		}
	}
}

// Heal restores full connectivity.
func (c *Cluster) Heal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = make(map[[2]int]bool)
}

// Reachable reports whether from can currently deliver a message to to.
func (c *Cluster) Reachable(from, to int) bool {
	if from == to {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.blocked[[2]int{from, to}]
}

// reachablePeers lists the ids reachable from the given node.
func (c *Cluster) reachablePeers(from int) []int {
	peers := make([]int, 0, len(c.nodes)-1)
	for id := range c.nodes {
		if id != from && c.Reachable(from, id) {
			peers = append(peers, id)
		}
	}
	return peers
}
