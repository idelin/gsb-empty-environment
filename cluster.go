package logrepl

import "fmt"

// Cluster is the discrete-time simulation environment. It owns the message
// queue and the link matrix; faults (partitions) and heartbeats are driven
// exclusively by calling Tick from the test clock.
type Cluster struct {
	nodes             map[int]*Node
	order             []int
	links             map[[2]int]bool
	queue             []Message
	Now               int
	HeartbeatInterval int
}

// NewCluster builds a cluster with deterministic, staggered election
// timeouts so tests are reproducible. All links start up.
func NewCluster(ids ...int) *Cluster {
	c := &Cluster{
		nodes:             make(map[int]*Node),
		links:             make(map[[2]int]bool),
		HeartbeatInterval: 4,
	}
	for i, id := range ids {
		c.nodes[id] = &Node{
			id:              id,
			role:            Follower,
			term:            0,
			votedFor:        None,
			leaderID:        None,
			log:             []Entry{{Term: 0}}, // sentinel
			electionTimeout: 10 + 2*i,
		}
		c.order = append(c.order, id)
	}
	for _, a := range ids {
		for _, b := range ids {
			if a != b {
				c.links[[2]int{a, b}] = true
			}
		}
	}
	return c
}

func (c *Cluster) quorum() int { return len(c.order)/2 + 1 }

// Node returns the node with the given id.
func (c *Cluster) Node(id int) *Node { return c.nodes[id] }

// Leader returns the id of the first node that currently believes it is
// leader, if any.
func (c *Cluster) Leader() (int, bool) {
	for _, id := range c.order {
		if c.nodes[id].role == Leader {
			return id, true
		}
	}
	return None, false
}

// SetLink enables or disables one directed link.
func (c *Cluster) SetLink(from, to int, up bool) { c.links[[2]int{from, to}] = up }

// Isolate cuts all links to and from id (a full network partition).
func (c *Cluster) Isolate(id int) {
	for _, other := range c.order {
		if other != id {
			c.SetLink(id, other, false)
			c.SetLink(other, id, false)
		}
	}
}

// Heal restores all links to and from id.
func (c *Cluster) Heal(id int) {
	for _, other := range c.order {
		if other != id {
			c.SetLink(id, other, true)
			c.SetLink(other, id, true)
		}
	}
}

// TriggerTimeout forces the node's election timer to fire on the next Tick.
// This is the test-clock hook used to trigger a view change deterministically.
func (c *Cluster) TriggerTimeout(id int) {
	n := c.nodes[id]
	n.electionElapsed = n.electionTimeout
}

// Inject queues a hand-crafted message (fault injection for tests).
func (c *Cluster) Inject(m Message) { c.queue = append(c.queue, m) }

// Propose appends a client command on the given node, which must be leader.
func (c *Cluster) Propose(id int, cmd string) error {
	n := c.nodes[id]
	if n.role != Leader {
		return fmt.Errorf("node %d is not leader (role=%s term=%d)", id, n.role, n.term)
	}
	n.log = append(n.log, Entry{Term: n.term, Cmd: cmd})
	n.matchIndex[id] = n.lastIndex()
	for _, peer := range c.order {
		if peer != id {
			n.sendAppend(c, peer)
		}
	}
	return nil
}

// Tick advances the whole cluster by one discrete time unit: first all
// queued messages are delivered (responses generated during delivery are
// delivered in the same tick), then every node's local clock fires.
func (c *Cluster) Tick() {
	c.Now++
	for len(c.queue) > 0 {
		m := c.queue[0]
		c.queue = c.queue[1:]
		if !c.links[[2]int{m.From, m.To}] {
			continue // dropped by the network
		}
		c.nodes[m.To].step(c, m)
	}
	for _, id := range c.order {
		c.nodes[id].tick(c)
	}
}

func (c *Cluster) send(m Message) { c.queue = append(c.queue, m) }
