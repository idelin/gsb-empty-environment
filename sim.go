package mvr

import (
	"container/heap"
	"math/rand"
	"sort"
)

// event is a scheduled action in the discrete-event simulator.
type event struct {
	at  int64
	seq int
	act func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim is a single-process discrete-event simulator. Communication delays,
// network partitions, node crashes and every node's wall clock are all
// controlled here, deterministically under one seed.
type Sim struct {
	now     int64
	seq     int
	queue   eventHeap
	rng     *rand.Rand
	nodes   map[string]*Node
	order   []string
	blocked map[[2]string]bool // directed links currently cut by a partition
}

// NewSim returns an empty simulator. All randomness derives from seed.
func NewSim(seed int64) *Sim {
	return &Sim{
		rng:     rand.New(rand.NewSource(seed)),
		nodes:   map[string]*Node{},
		blocked: map[[2]string]bool{},
	}
}

// Now is the simulator's logical time.
func (s *Sim) Now() int64 { return s.now }

// AddNode creates a replica. clockSkew shifts the node's wall clock away
// from the simulator clock, modelling unsynchronised machines.
func (s *Sim) AddNode(id string, clockSkew int64) *Node {
	n := &Node{ID: id, Reg: NewRegister(id), sim: s, up: true, skew: clockSkew}
	s.nodes[id] = n
	s.order = append(s.order, id)
	sort.Strings(s.order)
	return n
}

// Node returns the node with the given id.
func (s *Sim) Node(id string) *Node { return s.nodes[id] }

// Nodes lists all nodes in id order.
func (s *Sim) Nodes() []*Node {
	out := make([]*Node, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.nodes[id])
	}
	return out
}

// After schedules act to run delay time units from now.
func (s *Sim) After(delay int64, act func()) {
	heap.Push(&s.queue, event{at: s.now + delay, seq: s.seq, act: act})
	s.seq++
}

// Step executes the next scheduled event. It reports whether one ran.
func (s *Sim) Step() bool {
	if len(s.queue) == 0 {
		return false
	}
	e := heap.Pop(&s.queue).(event)
	s.now = e.at
	e.act()
	return true
}

// Run drains the event queue.
func (s *Sim) Run() {
	for s.Step() {
	}
}

// Partition cuts all traffic between a and b in both directions.
func (s *Sim) Partition(a, b string) {
	s.blocked[[2]string{a, b}] = true
	s.blocked[[2]string{b, a}] = true
}

// Heal restores traffic between a and b.
func (s *Sim) Heal(a, b string) {
	delete(s.blocked, [2]string{a, b})
	delete(s.blocked, [2]string{b, a})
}

// HealAll removes every partition.
func (s *Sim) HealAll() { s.blocked = map[[2]string]bool{} }

// send schedules delivery of a single entry; the network drops it silently
// if the link is partitioned, and the receiver drops it while crashed.
func (s *Sim) send(from, to string, e Entry) {
	if s.blocked[[2]string{from, to}] {
		return
	}
	s.After(1+s.rng.Int63n(5), func() {
		if n := s.nodes[to]; n.up {
			n.Reg.Apply(e)
		}
	})
}

// sendState schedules delivery of a full anti-entropy state.
func (s *Sim) sendState(from, to string, entries []Entry) {
	if s.blocked[[2]string{from, to}] {
		return
	}
	s.After(1+s.rng.Int63n(5), func() {
		if n := s.nodes[to]; n.up {
			n.Reg.Merge(entries)
		}
	})
}

// Converged reports whether every node holds the same entry set.
func (s *Sim) Converged() bool {
	ns := s.Nodes()
	for i := 1; i < len(ns); i++ {
		if !ns[0].Reg.Equal(ns[i].Reg) {
			return false
		}
	}
	return true
}

// Node is one replica of the register inside the simulator.
type Node struct {
	ID   string
	Reg  *Register
	sim  *Sim
	up   bool
	skew int64
}

// Wall is the node's local wall clock: simulator time plus its skew.
func (n *Node) Wall() int64 { return n.sim.now + n.skew }

// Up reports whether the node is running.
func (n *Node) Up() bool { return n.up }

// Crash stops the node. Its state survives, but it sends and receives
// nothing until Recover.
func (n *Node) Crash() { n.up = false }

// Recover brings a crashed node back with its state intact.
func (n *Node) Recover() { n.up = true }

// Write performs a local write and broadcasts it to all peers.
// It returns the produced entry.
func (n *Node) Write(value string) Entry {
	e := n.Reg.Write(value, n.Wall())
	n.broadcast(e)
	return e
}

// Resolve explicitly resolves the conflict set this node currently observes
// into value, and broadcasts the result so the resolution propagates.
func (n *Node) Resolve(value string) Entry {
	e := n.Reg.Resolve(value, n.Wall())
	n.broadcast(e)
	return e
}

func (n *Node) broadcast(e Entry) {
	if !n.up {
		return
	}
	for _, id := range n.sim.order {
		if id != n.ID {
			n.sim.send(n.ID, id, e)
		}
	}
}

// Sync starts one anti-entropy round: the node ships its full entry set to
// every peer. This repairs messages lost to partitions or crashes.
func (n *Node) Sync() {
	if !n.up {
		return
	}
	state := n.Reg.Entries()
	for _, id := range n.sim.order {
		if id != n.ID {
			n.sim.sendState(n.ID, id, state)
		}
	}
}

// SyncAll makes every live node run one anti-entropy round.
func (s *Sim) SyncAll() {
	for _, n := range s.Nodes() {
		n.Sync()
	}
}
