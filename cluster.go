package snaprepl

import "fmt"

// Cluster wires nodes, the event simulator, the faulty network and the
// safety monitor together.
type Cluster struct {
	Sim   *Sim
	Net   *Network
	Nodes []*Node
	Mon   *Monitor
	Trace func(format string, args ...interface{})
}

func NewCluster(seed int64, size int) *Cluster {
	sim := NewSim()
	net := NewNetwork(sim, seed)
	c := &Cluster{Sim: sim, Net: net, Mon: NewMonitor()}
	for i := 0; i < size; i++ {
		c.Nodes = append(c.Nodes, &Node{
			ID: i, sim: sim, cluster: c, Term: 1,
			SM: NewKV(), snapSend: map[int]*snapSend{},
			next: make([]int, size), match: make([]int, size),
		})
	}
	net.Deliver = func(to int, m Msg) { c.Nodes[to].Deliver(m) }
	leader := c.Nodes[0]
	leader.isLeader = true
	for i := range leader.next {
		leader.next[i] = 1
	}
	return c
}

func (c *Cluster) trace(format string, args ...interface{}) {
	if c.Trace != nil {
		c.Trace(format, args...)
	}
}

// StartHeartbeat makes the leader broadcast AppendEntries periodically.
func (c *Cluster) StartHeartbeat() {
	leader := c.Nodes[0]
	var beat func()
	beat = func() {
		leader.broadcast()
		c.Sim.After(heartbeatInterval, beat)
	}
	c.Sim.After(heartbeatInterval, beat)
}

// ClientWrites schedules count client writes, one every interval ticks.
func (c *Cluster) ClientWrites(count int, interval int64) {
	for i := 0; i < count; i++ {
		i := i
		c.Sim.After(1+int64(i)*interval, func() {
			c.Nodes[0].Propose(fmt.Sprintf("set key%d val%d", i%7, i))
		})
	}
}

// Converged reports whether every replica applied the same prefix and
// holds identical state machine content.
func (c *Cluster) Converged() bool {
	leader := c.Nodes[0]
	if leader.lastIndex() == 0 || leader.commitIndex != leader.lastIndex() ||
		leader.lastApplied != leader.lastIndex() {
		return false
	}
	for _, nd := range c.Nodes[1:] {
		if nd.lastApplied != leader.lastApplied || !nd.SM.Equal(leader.SM) {
			return false
		}
	}
	return true
}

// RunUntilConverged steps the simulator until all replicas converge or
// the virtual clock reaches limit. Convergence is only accepted at or
// after notBefore, so callers can wait out scheduled writes and fault
// windows. It returns the convergence time: the liveness evidence.
func (c *Cluster) RunUntilConverged(limit, notBefore int64) (int64, bool) {
	for c.Sim.Now() < limit {
		if c.Sim.Now() >= notBefore && c.Converged() {
			return c.Sim.Now(), true
		}
		if !c.Sim.Step() {
			break
		}
	}
	return c.Sim.Now(), c.Sim.Now() >= notBefore && c.Converged()
}
