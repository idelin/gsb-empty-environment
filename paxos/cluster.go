package paxos

import "sync"

// Cluster is a deterministic in-memory transport and clock harness.
//
// Messages sent by nodes are queued and delivered on the next Tick, so an
// entire execution is reproducible. Directed links can be blocked (fully
// or per message type) to simulate network partitions and message loss.
//
// Lock ordering: Cluster.mu is never held while calling into a Node, and
// Node.mu is never held while calling into the Cluster except for the
// leaf enqueue operation, so concurrent Tick/Propose cannot deadlock.
type Cluster struct {
	mu          sync.Mutex
	nodes       []*Node
	queue       []Message
	blocked     map[[2]int]bool // directed link fully blocked
	blockedType map[[3]int]bool // directed link blocked for one MsgType
	now         int
}

// NewCluster builds a cluster of size nodes sharing one deterministic
// clock and message queue.
func NewCluster(size int, cfg Config) *Cluster {
	c := &Cluster{
		nodes:       make([]*Node, size),
		blocked:     map[[2]int]bool{},
		blockedType: map[[3]int]bool{},
	}
	for i := 0; i < size; i++ {
		id := i
		send := func(to int, m Message) {
			m.To = to
			c.enqueue(m)
		}
		c.nodes[i] = newNode(id, size, cfg, send)
	}
	return c
}

// Node returns the i-th node.
func (c *Cluster) Node(i int) *Node { return c.nodes[i] }

// Size returns the number of nodes.
func (c *Cluster) Size() int { return len(c.nodes) }

// Now returns the current deterministic tick.
func (c *Cluster) Now() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Cluster) enqueue(m Message) {
	c.mu.Lock()
	c.queue = append(c.queue, m)
	c.mu.Unlock()
}

// Tick delivers every queued message (dropping the ones on blocked links)
// and then advances every node's clock by one tick.
func (c *Cluster) Tick() {
	c.mu.Lock()
	c.now++
	q := c.queue
	c.queue = nil
	blocked := make(map[[2]int]bool, len(c.blocked))
	for k, v := range c.blocked {
		blocked[k] = v
	}
	blockedType := make(map[[3]int]bool, len(c.blockedType))
	for k, v := range c.blockedType {
		blockedType[k] = v
	}
	nodes := c.nodes
	c.mu.Unlock()

	for _, m := range q {
		if blocked[[2]int{m.From, m.To}] ||
			blockedType[[3]int{m.From, m.To, int(m.Type)}] {
			continue
		}
		nodes[m.To].Handle(m)
	}
	for _, n := range nodes {
		n.OnTick()
	}
}

// TickN advances the cluster by n ticks.
func (c *Cluster) TickN(n int) {
	for i := 0; i < n; i++ {
		c.Tick()
	}
}

// Block drops all messages on the directed link from -> to.
func (c *Cluster) Block(from, to int) {
	c.mu.Lock()
	c.blocked[[2]int{from, to}] = true
	c.mu.Unlock()
}

// BlockType drops messages of the given type on the directed link.
func (c *Cluster) BlockType(from, to int, t MsgType) {
	c.mu.Lock()
	c.blockedType[[3]int{from, to, int(t)}] = true
	c.mu.Unlock()
}

// Unblock re-opens the directed link from -> to.
func (c *Cluster) Unblock(from, to int) {
	c.mu.Lock()
	delete(c.blocked, [2]int{from, to})
	delete(c.blockedType, [3]int{from, to, int(MsgPrepare)})
	delete(c.blockedType, [3]int{from, to, int(MsgPromise)})
	delete(c.blockedType, [3]int{from, to, int(MsgAccept)})
	delete(c.blockedType, [3]int{from, to, int(MsgAccepted)})
	delete(c.blockedType, [3]int{from, to, int(MsgHeartbeat)})
	c.mu.Unlock()
}

// Partition blocks every link between group a and group b (both
// directions). Links inside each group stay open.
func (c *Cluster) Partition(a, b []int) {
	for _, x := range a {
		for _, y := range b {
			c.Block(x, y)
			c.Block(y, x)
		}
	}
}

// Isolate cuts node id off from every other node.
func (c *Cluster) Isolate(id int) {
	for i := 0; i < len(c.nodes); i++ {
		if i != id {
			c.Block(id, i)
			c.Block(i, id)
		}
	}
}

// Heal re-opens every link.
func (c *Cluster) Heal() {
	c.mu.Lock()
	c.blocked = map[[2]int]bool{}
	c.blockedType = map[[3]int]bool{}
	c.mu.Unlock()
}

// Leader returns the id of the node that currently believes it is the
// leader, or -1 if there is none.
func (c *Cluster) Leader() int {
	for _, n := range c.nodes {
		if n.Role() == Leader {
			return n.ID()
		}
	}
	return -1
}
